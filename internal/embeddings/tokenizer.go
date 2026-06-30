// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package embeddings provides an in-process text embedding engine backed
// by the nomic-embed-text-v1.5 model whose weights are compiled into the
// baifo binary. It has no cgo and no heavyweight dependencies: the model
// is a small (~137M parameter) BERT-style encoder run in pure Go.
//
// The public surface is intentionally tiny: New() loads the embedded
// model once, and Embed/EmbedBatch turn text into 768-dimensional float32
// vectors suitable for cosine-similarity search.
package embeddings

import (
	"strings"
	"unicode"
)

// tokenizer is a WordPiece tokenizer matching the BERT uncased scheme
// used by nomic-embed-text: lowercasing, Chinese-character splitting,
// punctuation splitting, then greedy longest-match subword tokenization.
type tokenizer struct {
	vocab    map[string]int32
	unkID    int32
	clsID    int32
	sepID    int32
	maxChars int // max chars per word before it maps straight to [UNK]
}

func newTokenizer(vocabText string) *tokenizer {
	t := &tokenizer{vocab: make(map[string]int32, 30528), maxChars: 100}
	id := int32(0)
	for _, line := range strings.Split(vocabText, "\n") {
		tok := strings.TrimRight(line, "\r")
		if tok == "" && id >= 30522 {
			// trailing newline at EOF; stop counting padding
			continue
		}
		t.vocab[tok] = id
		id++
	}
	t.unkID = t.vocab["[UNK]"]
	t.clsID = t.vocab["[CLS]"]
	t.sepID = t.vocab["[SEP]"]
	return t
}

// encode turns text into token ids, including the leading [CLS] and
// trailing [SEP]. The result is truncated to maxLen total tokens.
func (t *tokenizer) encode(text string, maxLen int) []int32 {
	ids := make([]int32, 0, 32)
	ids = append(ids, t.clsID)
	for _, w := range t.basicTokenize(text) {
		if len(ids) >= maxLen-1 {
			break
		}
		t.wordpiece(w, &ids, maxLen)
	}
	if len(ids) > maxLen-1 {
		ids = ids[:maxLen-1]
	}
	ids = append(ids, t.sepID)
	return ids
}

// basicTokenize lowercases, strips control chars, splits on whitespace
// and punctuation, and isolates CJK characters into their own tokens —
// the same preprocessing BERT's BasicTokenizer performs.
func (t *tokenizer) basicTokenize(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		if r == 0 || r == 0xFFFD || isControl(r) {
			continue
		}
		if isWhitespace(r) {
			flush()
			continue
		}
		lr := unicode.ToLower(r)
		if isPunct(lr) || isCJK(lr) {
			flush()
			out = append(out, string(lr))
			continue
		}
		b.WriteRune(lr)
	}
	flush()
	return out
}

// wordpiece performs greedy longest-match subword tokenization of a
// single whitespace/punct-delimited word, appending ids to dst.
func (t *tokenizer) wordpiece(word string, dst *[]int32, maxLen int) {
	runes := []rune(word)
	if len(runes) > t.maxChars {
		*dst = append(*dst, t.unkID)
		return
	}
	start := 0
	var sub []int32
	for start < len(runes) {
		end := len(runes)
		var curID int32 = -1
		for start < end {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := t.vocab[piece]; ok {
				curID = id
				break
			}
			end--
		}
		if curID < 0 {
			// unknown subword: whole word becomes [UNK]
			*dst = append(*dst, t.unkID)
			return
		}
		sub = append(sub, curID)
		start = end
	}
	*dst = append(*dst, sub...)
}

func isWhitespace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	return unicode.IsSpace(r)
}

func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

func isPunct(r rune) bool {
	// BERT treats ASCII non-alphanumeric and all Unicode punctuation as punctuation.
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) ||
		(r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}
