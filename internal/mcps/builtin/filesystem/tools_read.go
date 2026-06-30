// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ls

// LsArgs is the input of the Ls tool.
type LsArgs struct {
	Path          string `json:"path"`
	Depth         int    `json:"depth,omitempty"`
	Pattern       string `json:"pattern,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
}

// LsEntry mirrors a filesystem entry returned by Ls. Output is flat
// (no nested children) so the schema inference works and the LLM gets
// a simpler shape to reason about. Tree structure is conveyed by the
// Depth field plus the lexicographic order of paths.
type LsEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Depth   int    `json:"depth"`
	Size    int64  `json:"size,omitempty"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
}

// LsResult is what the LLM receives.
type LsResult struct {
	Entries []LsEntry `json:"entries"`
}

// Ls lists directory contents up to args.Depth (default 1). Hidden
// entries are filtered out unless IncludeHidden is set. The Pattern is
// matched against file names (directories are always shown so the tree
// can be walked).
func (t *Tools) Ls(args LsArgs) (LsResult, error) {
	abs, err := resolvePath(args.Path)
	if err != nil {
		return LsResult{}, err
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 1
	}
	entries, err := listDir(abs, depth, 0, args.Pattern, args.IncludeHidden)
	if err != nil {
		return LsResult{}, fmt.Errorf("list directory: %w", err)
	}
	return LsResult{Entries: entries}, nil
}

// listDir is the recursive workhorse used by Ls. Entries from
// subdirectories are appended after the parent with their Depth bumped,
// producing a pre-order flattened view.
func listDir(dirPath string, maxDepth, currentDepth int, pattern string, includeHidden bool) ([]LsEntry, error) {
	if currentDepth >= maxDepth {
		return nil, nil
	}
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var entries []LsEntry
	for _, de := range dirEntries {
		name := de.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if pattern != "" && !de.IsDir() {
			matched, _ := filepath.Match(pattern, name)
			if !matched {
				continue
			}
		}
		full := filepath.Join(dirPath, name)
		info, err := de.Info()
		if err != nil {
			continue
		}
		entry := LsEntry{
			Name:    name,
			Path:    full,
			Type:    "file",
			Depth:   currentDepth,
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		}
		if de.IsDir() {
			entry.Type = "directory"
			entry.Size = 0
		}
		entries = append(entries, entry)
		if de.IsDir() {
			children, err := listDir(full, maxDepth, currentDepth+1, pattern, includeHidden)
			if err == nil {
				entries = append(entries, children...)
			}
		}
	}
	return entries, nil
}

// read_file

// ReadRange is one slice of lines to read.
type ReadRange struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// ReadFileArgs is the input of the ReadFile tool.
type ReadFileArgs struct {
	Path   string      `json:"path"`
	Ranges []ReadRange `json:"ranges,omitempty"`
}

// ReadFragment is a slice of lines, numbered for the LLM's benefit.
type ReadFragment struct {
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
	Lines      []string `json:"lines"`
	TotalLines int      `json:"total_lines"`
}

// ReadFileResult is what the LLM receives.
type ReadFileResult struct {
	Path           string         `json:"path"`
	TotalLines     int            `json:"total_lines"`
	Fragments      []ReadFragment `json:"fragments"`
	Truncated      bool           `json:"truncated,omitempty"`
	TruncationNote string         `json:"truncation_note,omitempty"`
}

// ReadFile reads a file in full or in ranges. Ranges are 0-based and
// each fragment is returned with line numbers prefixed so the LLM can
// reference them precisely without re-counting.
//
// When t.maxReadFileChars is non-zero, a character budget is maintained
// across all fragments. Once the budget is exhausted the current
// fragment's Limit is adjusted to the lines actually included, remaining
// ranges are dropped, and the result carries Truncated=true together with
// a TruncationNote that tells the model how to read the rest. Truncation
// happens at whole-line granularity, with one exception: when the very
// first line already exceeds the budget (single-huge-line files), its
// head is returned with an inline truncation marker so the result is
// never empty.
func (t *Tools) ReadFile(args ReadFileArgs) (ReadFileResult, error) {
	abs, err := resolvePath(args.Path)
	if err != nil {
		return ReadFileResult{}, err
	}

	allLines, err := readAllLines(abs)
	if err != nil {
		return ReadFileResult{}, err
	}
	total := len(allLines)

	if len(args.Ranges) == 0 {
		args.Ranges = []ReadRange{{Offset: 0, Limit: total}}
	}

	limited := t.maxReadFileChars > 0
	remaining := t.maxReadFileChars
	var capped bool
	lastLine := 0
	emitted := 0

	fragments := make([]ReadFragment, 0, len(args.Ranges))
	for _, r := range args.Ranges {
		if capped {
			break
		}
		offset := r.Offset
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		limit := r.Limit
		if limit <= 0 {
			limit = total - offset
		}
		end := offset + limit
		if end > total {
			end = total
		}
		frag := ReadFragment{
			Offset:     offset,
			Limit:      end - offset,
			Lines:      make([]string, 0, end-offset),
			TotalLines: total,
		}
		linesAdded := 0
		for i := offset; i < end; i++ {
			line := fmt.Sprintf("%d: %s", i, allLines[i])
			if limited {
				if remaining <= 0 {
					capped = true
					break
				}
				if len(line) > remaining {
					capped = true
					// Hard-truncate only when nothing has been emitted
					// yet, so a single huge line (e.g. a minified or
					// binary blob with no newlines) still returns a
					// useful head instead of an empty result. Otherwise
					// keep whole-line granularity and stop here.
					if emitted == 0 {
						line, _ = truncateString(line, remaining, "")
						frag.Lines = append(frag.Lines, line)
						linesAdded++
						emitted++
						lastLine = i
					}
					break
				}
				remaining -= len(line)
			}
			frag.Lines = append(frag.Lines, line)
			linesAdded++
			emitted++
			lastLine = i
		}
		if capped {
			frag.Limit = linesAdded
		}
		fragments = append(fragments, frag)
	}

	res := ReadFileResult{
		Path:       abs,
		TotalLines: total,
		Fragments:  fragments,
	}
	if capped {
		res.Truncated = true
		res.TruncationNote = fmt.Sprintf(
			"output capped at %d chars (file has %d lines, returned through line %d): "+
				"use ranges (offset+limit) to read the rest",
			t.maxReadFileChars, total, lastLine,
		)
	}
	return res, nil
}

// readAllLines reads the whole file into a slice of lines. We use a
// large scanner buffer to accept lines up to 10 MiB before failing.
func readAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return lines, nil
}

// system_info

// SystemInfoResult is what the LLM receives.
type SystemInfoResult struct {
	OS           string    `json:"os"`
	Architecture string    `json:"arch"`
	Hostname     string    `json:"hostname"`
	User         string    `json:"user"`
	WorkingDir   string    `json:"workdir"`
	NumCPU       int       `json:"num_cpu"`
	Time         time.Time `json:"time"`
}

// SystemInfo collects coarse environment info. The LLM is told via
// the tool description to call this ONLY on explicit user request;
// preemptive calls just spam the chat with system facts the user
// did not ask for.
func (t *Tools) SystemInfo() (SystemInfoResult, error) {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	usr := os.Getenv("USER")
	if usr == "" {
		usr = os.Getenv("USERNAME")
	}
	return SystemInfoResult{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		Hostname:     host,
		User:         usr,
		WorkingDir:   cwd,
		NumCPU:       runtime.NumCPU(),
		Time:         time.Now().UTC(),
	}, nil
}
