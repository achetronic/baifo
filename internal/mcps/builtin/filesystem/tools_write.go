// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Supported values for WriteFileArgs.Encoding.
const (
	EncodingUTF8   = "utf-8"
	EncodingBase64 = "base64"
)

// Supported values for WriteFileArgs.Mode.
const (
	WriteModeOverwrite = "overwrite"
	WriteModeAppend    = "append"
)

// WriteFileArgs is the input of the WriteFile tool.
//
// Two extra knobs vs the original filesystem-mcp version:
//
//   - Encoding lets the LLM submit content as base64 to dodge JSON
//     serialisation issues with backticks, newlines and embedded JSON.
//     The model seems to truncate / corrupt long literal strings in
//     tool-call payloads more often than long base64 strings.
//   - Mode lets the LLM grow a file incrementally without having to
//     fetch the previous content (which would require an extra
//     read_file turn).
type WriteFileArgs struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// WriteFileResult is what the LLM receives.
type WriteFileResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
	Mode         string `json:"mode"`
}

// WriteFile creates, overwrites or appends to a file. The previous
// content (if any) is snapshotted into the undo store before the write
// so the change can be reverted.
func (t *Tools) WriteFile(args WriteFileArgs) (WriteFileResult, error) {
	abs, err := resolvePath(args.Path)
	if err != nil {
		return WriteFileResult{}, err
	}

	data, err := decodeContent(args.Content, args.Encoding)
	if err != nil {
		return WriteFileResult{}, err
	}

	mode := args.Mode
	if mode == "" {
		mode = WriteModeOverwrite
	}

	if err := t.undo.Save(abs); err != nil {
		t.logger.Error("save undo state", "path", abs, "error", err)
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return WriteFileResult{}, fmt.Errorf("create parent directories: %w", err)
	}

	switch mode {
	case WriteModeOverwrite:
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return WriteFileResult{}, fmt.Errorf("write file: %w", err)
		}
	case WriteModeAppend:
		f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return WriteFileResult{}, fmt.Errorf("open for append: %w", err)
		}
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			return WriteFileResult{}, fmt.Errorf("append file: %w", err)
		}
	default:
		return WriteFileResult{}, fmt.Errorf("unknown mode %q (valid: %s, %s)", mode, WriteModeOverwrite, WriteModeAppend)
	}

	return WriteFileResult{
		Path:         abs,
		BytesWritten: len(data),
		Mode:         mode,
	}, nil
}

// decodeContent turns args.Content + args.Encoding into raw bytes.
// Empty encoding defaults to utf-8 (i.e. the literal string).
func decodeContent(content, encoding string) ([]byte, error) {
	switch encoding {
	case "", EncodingUTF8:
		return []byte(content), nil
	case EncodingBase64:
		// Be tolerant: agents sometimes wrap base64 in whitespace.
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
		if err != nil {
			return nil, fmt.Errorf("decode base64 content: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unknown encoding %q (valid: %s, %s)", encoding, EncodingUTF8, EncodingBase64)
	}
}

// edit_file

// EditOperation is one find-and-replace operation.
type EditOperation struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// EditFileArgs is the input of the EditFile tool.
type EditFileArgs struct {
	Path  string          `json:"path"`
	Edits []EditOperation `json:"edits"`
}

// EditOpResult reports the outcome of one EditOperation.
type EditOpResult struct {
	Index   int    `json:"index"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// EditFileResult is what the LLM receives.
type EditFileResult struct {
	Path         string         `json:"path"`
	EditsApplied int            `json:"edits_applied"`
	EditsFailed  int            `json:"edits_failed"`
	Results      []EditOpResult `json:"results"`
}

// EditFile applies a list of find-and-replace operations to a file.
// Edits are applied sequentially against the running content; an
// operation fails if old_text is missing or matches multiple times
// without replace_all=true. All-or-nothing semantics would be safer
// but harder to debug, so we surface per-operation results instead.
func (t *Tools) EditFile(args EditFileArgs) (EditFileResult, error) {
	abs, err := resolvePath(args.Path)
	if err != nil {
		return EditFileResult{}, err
	}
	if len(args.Edits) == 0 {
		return EditFileResult{}, fmt.Errorf("edits array is empty")
	}

	contentBytes, err := os.ReadFile(abs)
	if err != nil {
		return EditFileResult{}, fmt.Errorf("read file: %w", err)
	}
	if err := t.undo.Save(abs); err != nil {
		t.logger.Error("save undo state", "path", abs, "error", err)
	}

	content := string(contentBytes)
	results := make([]EditOpResult, len(args.Edits))
	applied := 0

	for i, edit := range args.Edits {
		results[i] = EditOpResult{Index: i}
		switch {
		case edit.OldText == "":
			results[i].Error = "old_text cannot be empty"
			continue
		case !strings.Contains(content, edit.OldText):
			results[i].Error = "old_text not found in file"
			continue
		case !edit.ReplaceAll && strings.Count(content, edit.OldText) > 1:
			results[i].Error = fmt.Sprintf(
				"old_text matches %d locations; use replace_all=true or provide more context",
				strings.Count(content, edit.OldText),
			)
			continue
		}

		if edit.ReplaceAll {
			content = strings.ReplaceAll(content, edit.OldText, edit.NewText)
		} else {
			content = strings.Replace(content, edit.OldText, edit.NewText, 1)
		}
		results[i].Success = true
		applied++
	}

	if applied > 0 {
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return EditFileResult{}, fmt.Errorf("write file after edits: %w", err)
		}
	}

	return EditFileResult{
		Path:         abs,
		EditsApplied: applied,
		EditsFailed:  len(args.Edits) - applied,
		Results:      results,
	}, nil
}

// undo

// UndoArgs is the input of the Undo tool.
type UndoArgs struct {
	Path string `json:"path"`
}

// UndoResult is what the LLM receives.
type UndoResult struct {
	Path string `json:"path"`
}

// Undo reverts the last write_file or edit_file operation on path.
func (t *Tools) Undo(args UndoArgs) (UndoResult, error) {
	abs, err := resolvePath(args.Path)
	if err != nil {
		return UndoResult{}, err
	}
	if err := t.undo.Restore(abs); err != nil {
		return UndoResult{}, err
	}
	return UndoResult{Path: abs}, nil
}
