// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package embeddings

import (
	_ "embed"
	"encoding/json"
	"math"
	"testing"
)

//go:embed testdata_ref.json
var refJSON []byte

// TestAgainstReference checks the Go int8 engine against full-precision
// float32 reference vectors computed offline (which themselves track the
// ollama nomic-embed-text oracle's semantic structure). int8 quantization
// costs us a little precision, so we require cosine >= 0.99.
func TestAgainstReference(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var ref map[string][]float32
	if err := json.Unmarshal(refJSON, &ref); err != nil {
		t.Fatalf("unmarshal ref: %v", err)
	}
	for text, want := range ref {
		got, err := e.Embed(text)
		if err != nil {
			t.Fatalf("Embed(%q): %v", text, err)
		}
		if len(got) != Dimension {
			t.Fatalf("Embed(%q): dim=%d, want %d", text, len(got), Dimension)
		}
		c := Cosine(got, want)
		if c < 0.99 {
			t.Errorf("Embed(%q): cosine vs reference = %.4f, want >= 0.99", text, c)
		} else {
			t.Logf("Embed(%q): cosine vs reference = %.5f", text, c)
		}
	}
}

// TestSemanticOrdering verifies that related sentences embed closer than
// unrelated ones — the property that actually matters for retrieval.
func TestSemanticOrdering(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cat, _ := e.Embed("A cat sat quietly on the warm mat.")
	dog, _ := e.Embed("A dog lay calmly on the soft rug.")
	fin, _ := e.Embed("The stock market crashed and investors panicked.")

	related := Cosine(cat, dog)
	unrelated := Cosine(cat, fin)
	if related <= unrelated {
		t.Errorf("expected related(cat,dog)=%.3f > unrelated(cat,finance)=%.3f", related, unrelated)
	}
	t.Logf("related=%.3f unrelated=%.3f", related, unrelated)
}

func TestNormalize(t *testing.T) {
	e, _ := New()
	v, err := e.EmbedNormalized("hello")
	if err != nil {
		t.Fatal(err)
	}
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if math.Abs(float64(sum)-1.0) > 1e-4 {
		t.Errorf("normalized vector L2^2 = %.6f, want 1.0", sum)
	}
}
