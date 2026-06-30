// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// search

// SearchArgs is the input of the Search tool.
type SearchArgs struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
	Include      string `json:"include,omitempty"`
	Exclude      string `json:"exclude,omitempty"`
	Literal      bool   `json:"literal,omitempty"`
	ContextLines int    `json:"context_lines,omitempty"`
	MaxResults   int    `json:"max_results,omitempty"`
}

// SearchMatch is one hit returned by Search.
type SearchMatch struct {
	File          string   `json:"file"`
	Line          int      `json:"line"`
	Content       string   `json:"content"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

// SearchResult is what the LLM receives.
type SearchResult struct {
	Matches        []SearchMatch `json:"matches"`
	TotalMatches   int           `json:"total_matches"`
	Truncated      bool          `json:"truncated"`
	TruncationNote string        `json:"truncation_note,omitempty"`
}

// matchSize estimates the character weight a match contributes to the
// result: the matched line plus its file path and any context lines.
// Used to enforce the search output-character cap at whole-match
// granularity.
func matchSize(m SearchMatch) int {
	n := len(m.File) + len(m.Content)
	for _, l := range m.ContextBefore {
		n += len(l)
	}
	for _, l := range m.ContextAfter {
		n += len(l)
	}
	return n
}

// Search walks the path tree and returns every line matching the regex.
// Literal=true escapes the pattern so the LLM can search for raw
// strings without learning regex syntax.
//
// Two independent limits bound the result. max_results caps the number
// of matches (default 100). When t.maxSearchOutputChars is non-zero, a
// character budget caps the total size of the matched lines and their
// context across all files: once a match would push the result past the
// budget it is dropped (at whole-match granularity, so a match is never
// split), remaining files are skipped, and the result carries
// Truncated=true with a TruncationNote telling the model how to narrow
// the query. The char budget is what keeps a broad pattern from
// flooding the context window even when it stays under max_results.
func (t *Tools) Search(args SearchArgs) (SearchResult, error) {
	if args.Pattern == "" {
		return SearchResult{}, fmt.Errorf("pattern is required")
	}
	abs, err := resolvePath(args.Path)
	if err != nil {
		return SearchResult{}, err
	}

	pattern := args.Pattern
	if args.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return SearchResult{}, fmt.Errorf("invalid regex: %w", err)
	}

	max := args.MaxResults
	if max <= 0 {
		max = 100
	}

	charLimit := t.maxSearchOutputChars
	used := 0
	var cappedByChars bool

	var matches []SearchMatch
	err = filepath.Walk(abs, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if args.Include != "" {
			if ok, _ := filepath.Match(args.Include, info.Name()); !ok {
				return nil
			}
		}
		if args.Exclude != "" {
			if ok, _ := filepath.Match(args.Exclude, info.Name()); ok {
				return nil
			}
		}
		if len(matches) >= max {
			return filepath.SkipAll
		}
		fileMatches, err := searchInFile(p, re, args.ContextLines, max-len(matches))
		if err != nil {
			return nil
		}
		for _, m := range fileMatches {
			if charLimit > 0 {
				// Keep at least one match so a single oversized hit still
				// returns something rather than an empty result.
				if len(matches) > 0 && used+matchSize(m) > charLimit {
					cappedByChars = true
					return filepath.SkipAll
				}
				used += matchSize(m)
			}
			matches = append(matches, m)
			if len(matches) >= max {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return SearchResult{}, fmt.Errorf("search: %w", err)
	}

	res := SearchResult{
		Matches:      matches,
		TotalMatches: len(matches),
	}
	switch {
	case cappedByChars:
		res.Truncated = true
		res.TruncationNote = fmt.Sprintf(
			"output capped at %d chars: refine the pattern or use include/exclude/max_results to narrow it",
			t.maxSearchOutputChars,
		)
	case len(matches) >= max:
		res.Truncated = true
	}
	return res, nil
}

func searchInFile(path string, re *regexp.Regexp, contextLines, limit int) ([]SearchMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var matches []SearchMatch
	for i, line := range lines {
		if len(matches) >= limit {
			break
		}
		if !re.MatchString(line) {
			continue
		}
		m := SearchMatch{
			File:    path,
			Line:    i,
			Content: strings.TrimRight(line, "\r\n"),
		}
		if contextLines > 0 {
			start := i - contextLines
			if start < 0 {
				start = 0
			}
			for j := start; j < i; j++ {
				m.ContextBefore = append(m.ContextBefore, fmt.Sprintf("%d: %s", j, lines[j]))
			}
			end := i + contextLines + 1
			if end > len(lines) {
				end = len(lines)
			}
			for j := i + 1; j < end; j++ {
				m.ContextAfter = append(m.ContextAfter, fmt.Sprintf("%d: %s", j, lines[j]))
			}
		}
		matches = append(matches, m)
	}
	return matches, nil
}

// diff

// DiffArgs is the input of the Diff tool.
type DiffArgs struct {
	PathA  string `json:"path_a"`
	PathB  string `json:"path_b"`
	StartA int    `json:"start_a,omitempty"`
	EndA   int    `json:"end_a,omitempty"`
	StartB int    `json:"start_b,omitempty"`
	EndB   int    `json:"end_b,omitempty"`
}

// DiffResult is what the LLM receives.
type DiffResult struct {
	PathA     string `json:"path_a"`
	PathB     string `json:"path_b"`
	Identical bool   `json:"identical"`
	Unified   string `json:"unified,omitempty"`
}

// Diff returns a unified-style diff between path_a and path_b (or
// slices of them when start/end are given). Implementation is an LCS
// table, which is fine for files up to a few thousand lines.
func (t *Tools) Diff(args DiffArgs) (DiffResult, error) {
	absA, err := resolvePath(args.PathA)
	if err != nil {
		return DiffResult{}, err
	}
	absB, err := resolvePath(args.PathB)
	if err != nil {
		return DiffResult{}, err
	}

	linesA, err := readAllLines(absA)
	if err != nil {
		return DiffResult{}, fmt.Errorf("read %s: %w", absA, err)
	}
	linesB, err := readAllLines(absB)
	if err != nil {
		return DiffResult{}, fmt.Errorf("read %s: %w", absB, err)
	}

	startA, endA := clampRange(args.StartA, args.EndA, len(linesA))
	startB, endB := clampRange(args.StartB, args.EndB, len(linesB))

	diff := computeDiff(absA, absB, linesA[startA:endA], linesB[startB:endB], startA, startB)
	return DiffResult{
		PathA:     absA,
		PathB:     absB,
		Identical: diff == "",
		Unified:   diff,
	}, nil
}

// clampRange normalises start/end to a valid slice of a length-n
// sequence. A zero end is treated as "until the end".
func clampRange(start, end, n int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end <= 0 || end > n {
		end = n
	}
	if start > end {
		start = end
	}
	return start, end
}

// computeDiff is a textbook LCS-based unified diff. Kept identical to
// the original filesystem-mcp implementation for behavioural parity.
func computeDiff(pathA, pathB string, linesA, linesB []string, offsetA, offsetB int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", pathA)
	fmt.Fprintf(&sb, "+++ %s\n", pathB)

	m, n := len(linesA), len(linesB)
	table := make([][]int, m+1)
	for i := range table {
		table[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			switch {
			case linesA[i-1] == linesB[j-1]:
				table[i][j] = table[i-1][j-1] + 1
			case table[i-1][j] >= table[i][j-1]:
				table[i][j] = table[i-1][j]
			default:
				table[i][j] = table[i][j-1]
			}
		}
	}

	type diffLine struct {
		op   byte
		text string
	}
	var diffs []diffLine
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && linesA[i-1] == linesB[j-1]:
			diffs = append(diffs, diffLine{' ', linesA[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || table[i][j-1] >= table[i-1][j]):
			diffs = append(diffs, diffLine{'+', linesB[j-1]})
			j--
		case i > 0:
			diffs = append(diffs, diffLine{'-', linesA[i-1]})
			i--
		}
	}
	// Reverse in place.
	for left, right := 0, len(diffs)-1; left < right; left, right = left+1, right-1 {
		diffs[left], diffs[right] = diffs[right], diffs[left]
	}

	hasChanges := false
	for _, d := range diffs {
		if d.op != ' ' {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return ""
	}
	for _, d := range diffs {
		fmt.Fprintf(&sb, "%c %s\n", d.op, d.text)
	}
	return sb.String()
}
