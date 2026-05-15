// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package embeddings

import (
	"math"
	"runtime"
	"sync"
)

// parallelFor splits the half-open range [0,n) across up to GOMAXPROCS
// workers and runs fn(start,end) on each chunk. For tiny n it runs inline
// to avoid goroutine overhead. Used to parallelize the per-token matmuls,
// which dominate the forward pass.
func parallelFor(n int, fn func(start, end int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	if workers <= 1 || n < 8 {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := start + chunk
		if end > n {
			end = n
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			fn(s, e)
		}(start, end)
	}
	wg.Wait()
}

// forward runs the encoder over a single token-id sequence and returns
// the mean-pooled, 768-dimensional sentence embedding (unnormalized).
//
// Architecture (nomic-embed-text-v1.5, NomicBertModel): post-norm BERT
// with rotary position embeddings (NeoX split, theta=1000), no learned
// position or attention/MLP biases, a SwiGLU MLP, and mean pooling over
// all token positions.
func (e *Engine) forward(ids []int32) []float32 {
	w := e.w
	h := w.hidden
	S := len(ids)

	// Token + token-type(0) embedding, then embedding LayerNorm.
	x := make([]float32, S*h)
	for i, id := range ids {
		emb := w.tokenEmb.row(int(id))
		dst := x[i*h : i*h+h]
		for j := 0; j < h; j++ {
			dst[j] = emb[j] + w.tokenType[j]
		}
	}
	layerNorm(x, S, h, w.embLNW, w.embLNB, w.lnEps)

	nh, hd := w.nHeads, w.headDim
	cos, sin := e.ropeTables(S)

	// Scratch buffers reused across layers. The per-token attention and
	// MLP loops are parallelized, so their temporaries (scores, inter,
	// inter2, mlpOut, attnOut) are allocated per worker inside the loop
	// rather than shared here.
	qkv := make([]float32, S*3*h)
	q := make([]float32, S*h)
	k := make([]float32, S*h)
	v := make([]float32, S*h)
	ctx := make([]float32, S*h)

	for li := range w.layers {
		l := &w.layers[li]

		// --- Self-attention ---
		// QKV projection per token (independent across tokens).
		parallelFor(S, func(start, end int) {
			for i := start; i < end; i++ {
				l.wqkv.matVec(x[i*h:i*h+h], qkv[i*3*h:i*3*h+3*h])
				row := qkv[i*3*h : i*3*h+3*h]
				copy(q[i*h:i*h+h], row[0:h])
				copy(k[i*h:i*h+h], row[h:2*h])
				copy(v[i*h:i*h+h], row[2*h:3*h])
			}
		})
		// Apply RoPE to q and k, per head.
		applyRoPE(q, S, nh, hd, cos, sin)
		applyRoPE(k, S, nh, hd, cos, sin)

		// Scaled dot-product attention, per query token (independent).
		scale := float32(1.0 / math.Sqrt(float64(hd)))
		parallelFor(S, func(start, end int) {
			scores := make([]float32, S)
			for i := start; i < end; i++ {
				cOut := ctx[i*h : i*h+h]
				for d := range cOut {
					cOut[d] = 0
				}
				for head := 0; head < nh; head++ {
					ho := head * hd
					qi := q[i*h+ho : i*h+ho+hd]
					// scores over all keys
					var maxS float32 = -math.MaxFloat32
					for j := 0; j < S; j++ {
						kj := k[j*h+ho : j*h+ho+hd]
						var dot float32
						for d := 0; d < hd; d++ {
							dot += qi[d] * kj[d]
						}
						dot *= scale
						scores[j] = dot
						if dot > maxS {
							maxS = dot
						}
					}
					var sum float32
					for j := 0; j < S; j++ {
						ex := float32(math.Exp(float64(scores[j] - maxS)))
						scores[j] = ex
						sum += ex
					}
					inv := 1.0 / sum
					// weighted sum of values
					for j := 0; j < S; j++ {
						wgt := scores[j] * inv
						vj := v[j*h+ho : j*h+ho+hd]
						for d := 0; d < hd; d++ {
							cOut[ho+d] += wgt * vj[d]
						}
					}
				}
			}
		})
		// out_proj and residual + norm1 (per token, independent).
		parallelFor(S, func(start, end int) {
			attnOut := make([]float32, h)
			for i := start; i < end; i++ {
				l.outProj.matVec(ctx[i*h:i*h+h], attnOut)
				xi := x[i*h : i*h+h]
				for d := 0; d < h; d++ {
					xi[d] += attnOut[d]
				}
			}
		})
		layerNorm(x, S, h, l.norm1W, l.norm1B, w.lnEps)

		// --- SwiGLU MLP: fc2( silu(fc12 x) * fc11 x ) --- (per token).
		parallelFor(S, func(start, end int) {
			inter := make([]float32, w.inter)
			inter2 := make([]float32, w.inter)
			mlpOut := make([]float32, h)
			for i := start; i < end; i++ {
				xi := x[i*h : i*h+h]
				l.fc11.matVec(xi, inter)  // gate value branch
				l.fc12.matVec(xi, inter2) // activation branch
				for d := 0; d < w.inter; d++ {
					g := inter2[d]
					silu := g / (1.0 + float32(math.Exp(float64(-g))))
					inter[d] = silu * inter[d]
				}
				l.fc2.matVec(inter, mlpOut)
				for d := 0; d < h; d++ {
					xi[d] += mlpOut[d]
				}
			}
		})
		layerNorm(x, S, h, l.norm2W, l.norm2B, w.lnEps)
	}

	// Mean pooling over all tokens.
	out := make([]float32, h)
	for i := 0; i < S; i++ {
		xi := x[i*h : i*h+h]
		for d := 0; d < h; d++ {
			out[d] += xi[d]
		}
	}
	invS := 1.0 / float32(S)
	for d := 0; d < h; d++ {
		out[d] *= invS
	}
	return out
}

// layerNorm normalizes each of the S rows of x (each length h) in place.
func layerNorm(x []float32, S, h int, gamma, beta []float32, eps float32) {
	for i := 0; i < S; i++ {
		row := x[i*h : i*h+h]
		var mean float32
		for _, v := range row {
			mean += v
		}
		mean /= float32(h)
		var variance float32
		for _, v := range row {
			d := v - mean
			variance += d * d
		}
		variance /= float32(h)
		inv := 1.0 / float32(math.Sqrt(float64(variance)+float64(eps)))
		for d := 0; d < h; d++ {
			row[d] = (row[d]-mean)*inv*gamma[d] + beta[d]
		}
	}
}

// ropeTables precomputes cos/sin for positions [0,S) over half the head
// dimension. Layout: cos[pos*half + i].
func (e *Engine) ropeTables(S int) (cos, sin []float32) {
	half := e.w.headDim / 2
	cos = make([]float32, S*half)
	sin = make([]float32, S*half)
	for pos := 0; pos < S; pos++ {
		for i := 0; i < half; i++ {
			freq := 1.0 / math.Pow(float64(e.w.ropeTheta), float64(i)/float64(half))
			ang := float64(pos) * freq
			cos[pos*half+i] = float32(math.Cos(ang))
			sin[pos*half+i] = float32(math.Sin(ang))
		}
	}
	return cos, sin
}

// applyRoPE rotates the per-head vectors in x in place using the NeoX
// split convention: the first half and second half of each head vector
// form the rotation pairs. x is [S, nh*hd] laid out row-major.
func applyRoPE(x []float32, S, nh, hd int, cos, sin []float32) {
	half := hd / 2
	h := nh * hd
	for pos := 0; pos < S; pos++ {
		for head := 0; head < nh; head++ {
			base := pos*h + head*hd
			for i := 0; i < half; i++ {
				c := cos[pos*half+i]
				s := sin[pos*half+i]
				x1 := x[base+i]
				x2 := x[base+half+i]
				x[base+i] = x1*c - x2*s
				x[base+half+i] = x2*c + x1*s
			}
		}
	}
}
