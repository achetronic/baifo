// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package facts implements baifo's long-term memory store.
package facts

import (
	"sync"
	"time"

	"github.com/achetronic/adk-utils-go/memory/memorytypes"

	"github.com/achetronic/baifo/internal/embeddings"
	"github.com/achetronic/baifo/internal/storage"
)

// FactEntry is the record.
type FactEntry struct {
	ID        uint64    `json:"id"`
	Content   string    `json:"content"`
	Category  string    `json:"category,omitempty"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
	Embedding []float32 `json:"-"` // Parsed from BLOB
}

// Store implements memorytypes.MemoryService and memorytypes.ExtendedMemoryService.
type Store struct {
	db  *storage.DB
	eng *embeddings.Engine
	mu  sync.Mutex
}

// New wraps the given storage handle and an optional embeddings engine.
func New(db *storage.DB, eng *embeddings.Engine) *Store {
	return &Store{db: db, eng: eng}
}

var (
	_ memorytypes.MemoryService         = (*Store)(nil)
	_ memorytypes.ExtendedMemoryService = (*Store)(nil)
)
