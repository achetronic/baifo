// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package embeddings

import (
	_ "embed"
	"errors"
	"math"
	"sync"
)

//go:embed assets/nomic-embed-text.weights
var weightBlob []byte

//go:embed assets/vocab.txt
var vocabText string

// Dimension is the size of every embedding vector this engine produces.
const Dimension = 768

// MaxTokens caps the sequence length fed to the model. nomic-embed-text
// supports up to 8192 via RoPE; we default to a sane 2048 to bound the
// O(n^2) attention cost, which is plenty for memory facts and snippets.
const MaxTokens = 2048

// Engine turns text into embedding vectors using the compiled-in
// nomic-embed-text model. It is safe for concurrent use: forward()
// allocates its own scratch buffers per call and the weights are
// read-only after construction.
type Engine struct {
	w   *weights
	tok *tokenizer
}

var (
	shared    *Engine
	sharedErr error
	once      sync.Once
)

// New builds (or returns the cached) Engine from the embedded weights.
// The model is loaded lazily and only once per process.
func New() (*Engine, error) {
	once.Do(func() {
		w, err := parseWeights(weightBlob)
		if err != nil {
			sharedErr = err
			return
		}
		shared = &Engine{w: w, tok: newTokenizer(vocabText)}
	})
	return shared, sharedErr
}

// Embed returns the embedding of a single piece of text.
func (e *Engine) Embed(text string) ([]float32, error) {
	if e == nil || e.w == nil {
		return nil, errors.New("embeddings: engine not initialised")
	}
	ids := e.tok.encode(text, MaxTokens)
	return e.forward(ids), nil
}

// EmbedBatch embeds several texts. Inputs are processed sequentially; the
// returned slice is index-aligned with texts.
func (e *Engine) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.Embed(t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// EmbedNormalized returns the L2-normalized embedding, convenient for
// cosine similarity via a plain dot product.
func (e *Engine) EmbedNormalized(text string) ([]float32, error) {
	v, err := e.Embed(text)
	if err != nil {
		return nil, err
	}
	Normalize(v)
	return v, nil
}

// Normalize scales v to unit L2 length in place (no-op for a zero vector).
func Normalize(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	inv := 1.0 / float32(math.Sqrt(float64(sum)))
	for i := range v {
		v[i] *= inv
	}
}

// Cosine returns the cosine similarity between two equal-length vectors.
func Cosine(a, b []float32) float32 {
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}
