// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package facts

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/embeddings"
)

// decodeEmbedding converts a BLOB back to a []float32.
func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4 : i*4+4]))
	}
	return out
}

// encodeEmbedding converts a []float32 to a BLOB.
func encodeEmbedding(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:i*4+4], math.Float32bits(f))
	}
	return b
}

type scoredEntry struct {
	entry FactEntry
	score float32
}

func (s *Store) searchEntriesSemantic(ctx context.Context, app, user, query string) ([]FactEntry, error) {
	// Pre-filter to the user's facts. Since personal facts usually number in the
	// tens or hundreds, pulling them into memory for cosine distance is extremely fast.
	r, err := s.db.SQL().QueryContext(ctx, `
		SELECT id, content, category, author, timestamp, embedding FROM facts
		WHERE app_name = ? AND user_id = ?
	`, app, user)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var entries []FactEntry
	var needsBackfill []FactEntry

	for r.Next() {
		var e FactEntry
		var tsStr string
		var embBytes []byte
		if err := r.Scan(&e.ID, &e.Content, &e.Category, &e.Author, &tsStr, &embBytes); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		e.Embedding = decodeEmbedding(embBytes)
		if e.Embedding == nil && e.Content != "" {
			needsBackfill = append(needsBackfill, e)
		}
		entries = append(entries, e)
	}
	r.Close()

	// Backfill missing embeddings (for facts created before this feature)
	if len(needsBackfill) > 0 {
		for _, e := range needsBackfill {
			v, err := s.eng.EmbedNormalized(e.Content)
			if err != nil {
				continue // skip on error
			}
			blob := encodeEmbedding(v)
			// Update the database and the in-memory list
			_, _ = s.db.SQL().ExecContext(ctx, "UPDATE facts SET embedding = ? WHERE id = ?", blob, e.ID)
			for i := range entries {
				if entries[i].ID == e.ID {
					entries[i].Embedding = v
					break
				}
			}
		}
	}

	// Embed the query
	qv, err := s.eng.EmbedNormalized("search_query: " + query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Score all entries
	var scored []scoredEntry
	for _, e := range entries {
		if e.Embedding == nil {
			continue // skip empty
		}
		score := embeddings.Cosine(qv, e.Embedding)
		scored = append(scored, scoredEntry{entry: e, score: score})
	}

	// Sort by highest cosine similarity
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top N (e.g. top 10)
	limit := 10
	if len(scored) < limit {
		limit = len(scored)
	}

	// Filter out really low scores if desired, but for now just return top N
	var out []FactEntry
	for i := 0; i < limit; i++ {
		// Only return reasonably relevant items
		if scored[i].score > 0.4 {
			out = append(out, scored[i].entry)
		}
	}

	return out, nil
}

// searchEntries returns every entry of (app, user) matching query.
// If an embeddings engine is configured, it performs semantic search via cosine
// similarity on the precomputed embeddings (computing them on-the-fly for older
// entries that lack them). Otherwise, it falls back to substring match.
func (s *Store) searchEntries(ctx context.Context, app, user, query string) ([]FactEntry, error) {
	if s.eng != nil && query != "" {
		return s.searchEntriesSemantic(ctx, app, user, query)
	}

	var rows *sqlRowsWrapper
	var queryErr error

	if query == "" {
		r, err := s.db.SQL().QueryContext(ctx, `
			SELECT id, content, category, author, timestamp FROM facts
			WHERE app_name = ? AND user_id = ?
			ORDER BY timestamp DESC;
		`, app, user)
		if err != nil {
			return nil, err
		}
		rows = &sqlRowsWrapper{rows: r}
	} else {
		likeQuery := "%" + query + "%"
		r, err := s.db.SQL().QueryContext(ctx, `
			SELECT id, content, category, author, timestamp FROM facts
			WHERE app_name = ? AND user_id = ?
			AND (content LIKE ? OR category LIKE ? OR author LIKE ?)
			ORDER BY timestamp DESC;
		`, app, user, likeQuery, likeQuery, likeQuery)
		if err != nil {
			return nil, err
		}
		rows = &sqlRowsWrapper{rows: r}
	}
	defer rows.Close()

	var out []FactEntry
	for rows.Next() {
		var e FactEntry
		var tsStr string
		if err := rows.Scan(&e.ID, &e.Content, &e.Category, &e.Author, &tsStr); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		out = append(out, e)
	}
	if queryErr != nil {
		return nil, queryErr
	}
	return out, nil
}

type sqlRowsWrapper struct {
	rows interface {
		Next() bool
		Scan(dest ...any) error
		Close() error
	}
}

func (w *sqlRowsWrapper) Next() bool {
	return w.rows.Next()
}

func (w *sqlRowsWrapper) Scan(dest ...any) error {
	return w.rows.Scan(dest...)
}

func (w *sqlRowsWrapper) Close() error {
	return w.rows.Close()
}

// contentText extracts the first non-empty text part from a
// genai.Content, joining multiple parts with newlines.
func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range c.Parts {
		if p == nil || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.Text)
	}
	return b.String()
}
