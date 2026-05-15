// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package filesystem

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTools(t *testing.T) *Tools {
	t.Helper()
	return New(Config{})
}

func TestWriteAndReadFile(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "hello\nworld\n"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res, err := tools.ReadFile(ReadFileArgs{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if res.TotalLines != 2 {
		t.Errorf("TotalLines: got %d, want 2", res.TotalLines)
	}
}

func TestWriteFileBase64Encoding(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	plain := "weird \x00 bytes and \n newlines and backticks"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: encoded, Encoding: EncodingBase64}); err != nil {
		t.Fatalf("WriteFile (base64): %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(data) != plain {
		t.Errorf("decoded content mismatch: got %q want %q", string(data), plain)
	}
}

func TestWriteFileAppendMode(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.md")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "head\n"}); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "body\n", Mode: WriteModeAppend}); err != nil {
		t.Fatalf("append second chunk: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "head\nbody\n" {
		t.Errorf("append result mismatch: got %q", data)
	}
}

func TestWriteFileRejectsUnknownEncoding(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "x", Encoding: "rot13"}); err == nil {
		t.Error("expected error for unknown encoding")
	}
}

func TestWriteFileRejectsUnknownMode(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "x", Mode: "merge"}); err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestUndoRestoresPreviousContent(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "u.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "v1"}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "v2"}); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if _, err := tools.Undo(UndoArgs{Path: path}); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v1" {
		t.Errorf("Undo did not restore v1: got %q", data)
	}
}

func TestEditFileFindAndReplace(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "e.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "hello world"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res, err := tools.EditFile(EditFileArgs{
		Path:  path,
		Edits: []EditOperation{{OldText: "world", NewText: "baifo"}},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if res.EditsApplied != 1 || res.EditsFailed != 0 {
		t.Errorf("counts: %+v", res)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello baifo" {
		t.Errorf("content: got %q", data)
	}
}

func TestEditFileRejectsAmbiguousMatch(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "e.txt")
	if _, err := tools.WriteFile(WriteFileArgs{Path: path, Content: "a a a"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res, err := tools.EditFile(EditFileArgs{
		Path:  path,
		Edits: []EditOperation{{OldText: "a", NewText: "b"}},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if res.EditsApplied != 0 || res.EditsFailed != 1 {
		t.Errorf("expected 0/1, got %+v", res)
	}
	if !strings.Contains(res.Results[0].Error, "matches 3 locations") {
		t.Errorf("error message: %q", res.Results[0].Error)
	}
}

func TestLsReportsFiles(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("b"), 0o600)
	res, err := tools.Ls(LsArgs{Path: dir})
	if err != nil {
		t.Fatalf("Ls: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(res.Entries))
	}
}

func TestSearchFindsPattern(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo\nbar\nfoobar\n"), 0o600)
	res, err := tools.Search(SearchArgs{Pattern: "foo", Path: dir, Literal: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalMatches != 2 {
		t.Errorf("matches: got %d, want 2", res.TotalMatches)
	}
}

func TestDiffIdentical(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	_ = os.WriteFile(a, []byte("same\n"), 0o600)
	_ = os.WriteFile(b, []byte("same\n"), 0o600)
	res, err := tools.Diff(DiffArgs{PathA: a, PathB: b})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.Identical {
		t.Error("expected identical=true")
	}
}

func TestSanitizePathRejectsBraceExpansion(t *testing.T) {
	if err := sanitizePath("/tmp/{a,b}/x"); err == nil {
		t.Error("expected error for brace expansion")
	}
	if err := sanitizePath("/tmp/x"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestScratchSetGetDelete(t *testing.T) {
	tools := newTools(t)
	if _, err := tools.Scratch(ScratchArgs{Action: ScratchSet, Key: "k", Value: "v"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := tools.Scratch(ScratchArgs{Action: ScratchGet, Key: "k"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "v" {
		t.Errorf("Get: got %q", got.Value)
	}
	if _, err := tools.Scratch(ScratchArgs{Action: ScratchDelete, Key: "k"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tools.Scratch(ScratchArgs{Action: ScratchGet, Key: "k"}); err == nil {
		t.Error("expected Get-after-Delete to fail")
	}
}

func TestExecForeground(t *testing.T) {
	tools := newTools(t)
	res, err := tools.Exec(ExecArgs{Command: "echo hello"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode: %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("Stdout: %q", res.Stdout)
	}
}

func TestADKToolsBuildEveryTool(t *testing.T) {
	tools := newTools(t)
	list, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	if len(list) != 12 {
		t.Errorf("ADKTools count: got %d, want 12", len(list))
	}
	seen := map[string]bool{}
	for _, tl := range list {
		if seen[tl.Name()] {
			t.Errorf("duplicate tool name %q", tl.Name())
		}
		seen[tl.Name()] = true
		if tl.Description() == "" {
			t.Errorf("tool %q has empty description", tl.Name())
		}
	}
}

func TestExecTruncatesLongStdout(t *testing.T) {
	tools := New(Config{MaxExecOutputChars: 10})
	// Generate output longer than 10 chars.
	res, _ := tools.Exec(ExecArgs{Command: "printf '%020d' 0"})
	if !res.StdoutTruncated {
		t.Errorf("expected StdoutTruncated=true, got false; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[truncated:") {
		t.Errorf("expected truncation marker in stdout: %q", res.Stdout)
	}
}

func TestExecNoTruncationWhenUnlimited(t *testing.T) {
	tools := New(Config{MaxExecOutputChars: 0}) // 0 = unlimited
	res, err := tools.Exec(ExecArgs{Command: "echo hello"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.StdoutTruncated {
		t.Error("expected StdoutTruncated=false when cap=0")
	}
	if strings.Contains(res.Stdout, "[truncated:") {
		t.Errorf("unexpected truncation marker: %q", res.Stdout)
	}
}

func TestReadFileTruncatesAtBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// Write 10 lines of 20 chars each.
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "line%02d_123456789\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Budget of 30 chars — enough for roughly 1-2 numbered lines.
	tools := New(Config{MaxReadFileChars: 30})
	res, err := tools.ReadFile(ReadFileArgs{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true")
	}
	if res.TruncationNote == "" {
		t.Error("expected non-empty TruncationNote")
	}
	if !strings.Contains(res.TruncationNote, "offset+limit") {
		t.Errorf("TruncationNote missing 'offset+limit': %q", res.TruncationNote)
	}
	// Fragment's Limit must equal the number of lines actually returned.
	if len(res.Fragments) == 0 {
		t.Fatal("no fragments")
	}
	frag := res.Fragments[0]
	if frag.Limit != len(frag.Lines) {
		t.Errorf("frag.Limit=%d but len(frag.Lines)=%d", frag.Limit, len(frag.Lines))
	}
	// Must not have returned all 10 lines.
	if len(frag.Lines) >= 10 {
		t.Errorf("expected fewer than 10 lines, got %d", len(frag.Lines))
	}
}

func TestReadFileNoTruncationWhenUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	_ = os.WriteFile(path, []byte("a\nb\nc\n"), 0o600)

	tools := New(Config{MaxReadFileChars: 0}) // 0 = unlimited
	res, err := tools.ReadFile(ReadFileArgs{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if res.Truncated {
		t.Error("expected Truncated=false when cap=0")
	}
	if res.TotalLines != 3 {
		t.Errorf("TotalLines: got %d, want 3", res.TotalLines)
	}
}

func TestADKToolDescriptionsContainCap(t *testing.T) {
	tools := New(Config{MaxExecOutputChars: 1234, MaxReadFileChars: 5678, MaxSearchOutputChars: 4321})
	list, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	byName := map[string]string{}
	for _, tl := range list {
		byName[tl.Name()] = tl.Description()
	}
	if !strings.Contains(byName["exec"], "1234") {
		t.Errorf("exec description missing cap 1234: %q", byName["exec"])
	}
	if !strings.Contains(byName["read_file"], "5678") {
		t.Errorf("read_file description missing cap 5678: %q", byName["read_file"])
	}
	if !strings.Contains(byName["process_status"], "1234") {
		t.Errorf("process_status description missing cap 1234: %q", byName["process_status"])
	}
	if !strings.Contains(byName["search"], "4321") {
		t.Errorf("search description missing cap 4321: %q", byName["search"])
	}
}

func TestADKToolDescriptionsOmitCapWhenUnlimited(t *testing.T) {
	tools := New(Config{MaxExecOutputChars: 0, MaxReadFileChars: 0})
	list, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	byName := map[string]string{}
	for _, tl := range list {
		byName[tl.Name()] = tl.Description()
	}
	if strings.Contains(byName["exec"], "capped") {
		t.Errorf("exec description should not mention cap when unlimited: %q", byName["exec"])
	}
	if strings.Contains(byName["read_file"], "capped") {
		t.Errorf("read_file description should not mention cap when unlimited: %q", byName["read_file"])
	}
	if strings.Contains(byName["process_status"], "capped") {
		t.Errorf("process_status description should not mention cap when unlimited: %q", byName["process_status"])
	}
	if strings.Contains(byName["search"], "capped") {
		t.Errorf("search description should not mention cap when unlimited: %q", byName["search"])
	}
}

func TestReadFileBudgetExactExhaustionStillCaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.txt")
	// Each numbered line is "N: a" = 4 chars; budget 8 fits exactly two
	// lines. The third line must be capped: a budget that hits exactly 0
	// must NOT be confused with the 0=unlimited sentinel.
	_ = os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o600)

	tools := New(Config{MaxReadFileChars: 8})
	res, err := tools.ReadFile(ReadFileArgs{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true on exact budget exhaustion")
	}
	if got := len(res.Fragments[0].Lines); got != 2 {
		t.Errorf("expected exactly 2 lines, got %d: %v", got, res.Fragments[0].Lines)
	}
}

func TestReadFileSingleHugeLineReturnsTruncatedHead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.txt")
	// One line far bigger than the budget (the binary-blob / minified-file
	// case). The result must NOT be empty: the head of the line comes back
	// with an inline truncation marker.
	_ = os.WriteFile(path, []byte(strings.Repeat("x", 1000)+"\n"), 0o600)

	tools := New(Config{MaxReadFileChars: 50})
	res, err := tools.ReadFile(ReadFileArgs{Path: path})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(res.Fragments) == 0 || len(res.Fragments[0].Lines) != 1 {
		t.Fatalf("expected exactly one (truncated) line, got %+v", res.Fragments)
	}
	line := res.Fragments[0].Lines[0]
	if !strings.Contains(line, "[truncated:") {
		t.Errorf("expected inline truncation marker, got %q", line)
	}
	if !strings.HasPrefix(line, "0: xxx") {
		t.Errorf("expected the head of the line, got %q", line)
	}
}

func TestSearchCapsOutputChars(t *testing.T) {
	dir := t.TempDir()
	// Many matching lines across files: each match is small, but the sum
	// blows a tight budget. The cap must stop well before all matches are
	// returned and flag the truncation.
	for i := 0; i < 5; i++ {
		var sb strings.Builder
		for j := 0; j < 20; j++ {
			sb.WriteString("needle on a reasonably long line of text\n")
		}
		path := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Budget of 200 chars fits only a handful of the ~40-char matches.
	tools := New(Config{MaxSearchOutputChars: 200})
	res, err := tools.Search(SearchArgs{Pattern: "needle", Path: dir, Literal: true, MaxResults: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when the char budget is exceeded")
	}
	if res.TruncationNote == "" {
		t.Error("expected a non-empty TruncationNote")
	}
	if res.TotalMatches >= 100 {
		t.Errorf("expected the cap to stop well before 100 matches, got %d", res.TotalMatches)
	}
	if res.TotalMatches == 0 {
		t.Error("expected at least one match returned")
	}
}

func TestSearchNoTruncationWhenUnlimited(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo\nfoo\nfoo\n"), 0o600)

	tools := New(Config{MaxSearchOutputChars: 0}) // 0 = unlimited
	res, err := tools.Search(SearchArgs{Pattern: "foo", Path: dir, Literal: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Truncated {
		t.Error("expected Truncated=false when cap=0")
	}
	if res.TotalMatches != 3 {
		t.Errorf("matches: got %d, want 3", res.TotalMatches)
	}
}

func TestSearchKeepsAtLeastOneMatchOverBudget(t *testing.T) {
	dir := t.TempDir()
	// A single match far larger than the budget must still come back, so
	// the tool never returns an empty result on a legitimate hit.
	line := "needle " + strings.Repeat("x", 500)
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte(line+"\n"), 0o600)

	tools := New(Config{MaxSearchOutputChars: 50})
	res, err := tools.Search(SearchArgs{Pattern: "needle", Path: dir, Literal: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalMatches != 1 {
		t.Errorf("expected exactly 1 match returned even over budget, got %d", res.TotalMatches)
	}
}
