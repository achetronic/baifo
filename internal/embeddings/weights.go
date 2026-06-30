// SPDX-License-Identifier: Apache-2.0

package embeddings

import (
	"encoding/binary"
	"fmt"
	"math"
)

// qmat is an int8-quantized weight matrix with per-row (output-channel)
// float32 scales. Logical shape is [rows, cols]; element (r,c) decodes to
// data[r*cols+c] * scale[r]. This is the format produced by the offline
// converter and keeps the embedded blob at ~132MB instead of ~547MB while
// preserving cosine similarity to within ~0.998 of the full-precision model.
type qmat struct {
	rows, cols int
	data       []int8
	scale      []float32
}

// matVec computes y = W * x for a [rows,cols] matrix and a length-cols
// vector, dequantizing on the fly. y must have length rows.
func (m *qmat) matVec(x []float32, y []float32) {
	for r := 0; r < m.rows; r++ {
		base := r * m.cols
		var acc float32
		row := m.data[base : base+m.cols]
		for c := 0; c < m.cols; c++ {
			acc += float32(row[c]) * x[c]
		}
		y[r] = acc * m.scale[r]
	}
}

// row returns a freshly dequantized copy of row r (used for the token
// embedding lookup table).
func (m *qmat) row(r int) []float32 {
	out := make([]float32, m.cols)
	base := r * m.cols
	s := m.scale[r]
	for c := 0; c < m.cols; c++ {
		out[c] = float32(m.data[base+c]) * s
	}
	return out
}

type layer struct {
	wqkv    *qmat // [3*hidden, hidden]
	outProj *qmat // [hidden, hidden]
	fc11    *qmat // [inter, hidden]
	fc12    *qmat // [inter, hidden]
	fc2     *qmat // [hidden, inter]
	norm1W  []float32
	norm1B  []float32
	norm2W  []float32
	norm2B  []float32
}

type weights struct {
	hidden, nLayers, nHeads, vocab, inter int
	headDim                               int
	ropeTheta                             float32
	lnEps                                 float32

	tokenEmb  *qmat     // [vocab, hidden]
	tokenType []float32 // [hidden] (token_type 0)
	embLNW    []float32
	embLNB    []float32
	layers    []layer
}

// blobReader is a tiny sequential reader over the embedded weight blob.
type blobReader struct {
	b   []byte
	off int
}

func (r *blobReader) i32() int {
	v := int(int32(binary.LittleEndian.Uint32(r.b[r.off:])))
	r.off += 4
	return v
}
func (r *blobReader) f32() float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(r.b[r.off:]))
	r.off += 4
	return v
}
func (r *blobReader) f32s(n int) []float32 {
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(r.b[r.off:]))
		r.off += 4
	}
	return out
}

// qmatOf reads a [rows,cols] int8 block followed by rows float32 scales.
func (r *blobReader) qmatOf(rows, cols int) *qmat {
	n := rows * cols
	data := make([]int8, n)
	for i := 0; i < n; i++ {
		data[i] = int8(r.b[r.off+i])
	}
	r.off += n
	scale := r.f32s(rows)
	return &qmat{rows: rows, cols: cols, data: data, scale: scale}
}

// parseWeights decodes the embedded blob produced by convert.py.
func parseWeights(b []byte) (*weights, error) {
	if len(b) < 4 || string(b[:4]) != "NMB1" {
		return nil, fmt.Errorf("embeddings: bad weight blob magic")
	}
	r := &blobReader{b: b, off: 4}
	w := &weights{}
	w.hidden = r.i32()
	w.nLayers = r.i32()
	w.nHeads = r.i32()
	w.vocab = r.i32()
	w.inter = r.i32()
	w.ropeTheta = r.f32()
	w.lnEps = r.f32()
	w.headDim = w.hidden / w.nHeads

	w.tokenEmb = r.qmatOf(w.vocab, w.hidden)
	w.tokenType = r.f32s(w.hidden)
	w.embLNW = r.f32s(w.hidden)
	w.embLNB = r.f32s(w.hidden)

	w.layers = make([]layer, w.nLayers)
	for i := 0; i < w.nLayers; i++ {
		l := &w.layers[i]
		l.wqkv = r.qmatOf(3*w.hidden, w.hidden)
		l.outProj = r.qmatOf(w.hidden, w.hidden)
		l.fc11 = r.qmatOf(w.inter, w.hidden)
		l.fc12 = r.qmatOf(w.inter, w.hidden)
		l.fc2 = r.qmatOf(w.hidden, w.inter)
		l.norm1W = r.f32s(w.hidden)
		l.norm1B = r.f32s(w.hidden)
		l.norm2W = r.f32s(w.hidden)
		l.norm2B = r.f32s(w.hidden)
	}
	if r.off != len(b) {
		return nil, fmt.Errorf("embeddings: weight blob size mismatch: parsed %d of %d bytes", r.off, len(b))
	}
	return w, nil
}
