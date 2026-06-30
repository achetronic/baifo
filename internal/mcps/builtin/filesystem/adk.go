// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ADKTools returns every tool of the built-in filesystem MCP wrapped
// as a google.golang.org/adk tool.Tool. The names match the original
// filesystem-mcp tool names so existing prompts keep working.
//
// Each tool description targets the LLM and documents quirks the model
// needs to know — especially the encoding/mode parameters of write_file
// that were added to dodge JSON tool-call corruption with long content.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	defs := []toolDef{
		{
			name:        "system_info",
			description: "Return OS, architecture, hostname, user, working directory and current time. Call ONLY when the user explicitly asks about the environment (e.g. \"what OS am I on?\"). Never call preemptively or to 'orient yourself' — the user does not see your reasoning, only the noise this tool generates.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{Name: "system_info", Description: "Return OS, architecture, hostname, user, working directory and current time. Call ONLY when the user explicitly asks about the environment. Never call preemptively."},
					func(_ tool.Context, _ struct{}) (SystemInfoResult, error) {
						return t.SystemInfo()
					},
				)
			},
		},
		{
			name: "ls",
			description: "List directory contents with optional depth, glob pattern filter, and hidden " +
				"file inclusion. Use depth=1 for a flat listing, depth>1 for a tree view.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "ls",
						Description: "List directory contents with optional depth, glob pattern filter, and hidden " +
							"file inclusion. Use depth=1 for a flat listing, depth>1 for a tree view.",
					},
					func(_ tool.Context, a LsArgs) (LsResult, error) { return t.Ls(a) },
				)
			},
		},
		{
			name:        "read_file",
			description: readFileDescription(t.maxReadFileChars),
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name:        "read_file",
						Description: readFileDescription(t.maxReadFileChars),
					},
					func(_ tool.Context, a ReadFileArgs) (ReadFileResult, error) { return t.ReadFile(a) },
				)
			},
		},
		{
			name: "write_file",
			description: "Create, overwrite or append to a file. Mode is 'overwrite' (default) or 'append'. " +
				"Encoding is 'utf-8' (default) or 'base64' — prefer base64 when the content contains many " +
				"backticks, embedded JSON, or other characters that may corrupt your own tool-call payload. " +
				"For very large files, write the first chunk with mode=overwrite and then keep calling with " +
				"mode=append.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "write_file",
						Description: "Create, overwrite or append to a file. Mode is 'overwrite' (default) or 'append'. " +
							"Encoding is 'utf-8' (default) or 'base64' — prefer base64 when the content contains many " +
							"backticks, embedded JSON, or other characters that may corrupt your own tool-call payload. " +
							"For very large files, write the first chunk with mode=overwrite and then keep calling with " +
							"mode=append.",
					},
					func(_ tool.Context, a WriteFileArgs) (WriteFileResult, error) { return t.WriteFile(a) },
				)
			},
		},
		{
			name: "edit_file",
			description: "Apply one or more find-and-replace edits to a file. Edits are applied " +
				"sequentially. old_text must match exactly. Reports which edits succeeded and which failed.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "edit_file",
						Description: "Apply one or more find-and-replace edits to a file. Edits are applied " +
							"sequentially. old_text must match exactly. Reports which edits succeeded and which failed.",
					},
					func(_ tool.Context, a EditFileArgs) (EditFileResult, error) { return t.EditFile(a) },
				)
			},
		},
		{
			name:        "search",
			description: searchDescription(t.maxSearchOutputChars),
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name:        "search",
						Description: searchDescription(t.maxSearchOutputChars),
					},
					func(_ tool.Context, a SearchArgs) (SearchResult, error) { return t.Search(a) },
				)
			},
		},
		{
			name:        "diff",
			description: "Compare two files (or sections of them) and return a unified diff.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{Name: "diff", Description: "Compare two files (or sections of them) and return a unified diff."},
					func(_ tool.Context, a DiffArgs) (DiffResult, error) { return t.Diff(a) },
				)
			},
		},
		{
			name:        "exec",
			description: execDescription(t.maxExecOutputChars),
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name:        "exec",
						Description: execDescription(t.maxExecOutputChars),
					},
					func(_ tool.Context, a ExecArgs) (ExecResult, error) { return t.Exec(a) },
				)
			},
		},
		{
			name:        "process_status",
			description: processStatusDescription(t.maxExecOutputChars),
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{Name: "process_status", Description: processStatusDescription(t.maxExecOutputChars)},
					func(_ tool.Context, a ProcessStatusArgs) (ProcessStatusResult, error) { return t.ProcessStatus(a) },
				)
			},
		},
		{
			name:        "process_kill",
			description: "Kill a background process by id.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{Name: "process_kill", Description: "Kill a background process by id."},
					func(_ tool.Context, a ProcessKillArgs) (ProcessKillResult, error) { return t.ProcessKill(a) },
				)
			},
		},
		{
			name: "scratch",
			description: "In-memory key-value store. Use to save snippets / plans / intermediate results " +
				"between tool calls without retransmitting them. action ∈ {set, get, delete, list}.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "scratch",
						Description: "In-memory key-value store. Use to save snippets / plans / intermediate results " +
							"between tool calls without retransmitting them. action in {set, get, delete, list}.",
					},
					func(_ tool.Context, a ScratchArgs) (ScratchResult, error) { return t.Scratch(a) },
				)
			},
		},
		{
			name:        "undo",
			description: "Undo the last write_file or edit_file operation on the given path.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{Name: "undo", Description: "Undo the last write_file or edit_file operation on the given path."},
					func(_ tool.Context, a UndoArgs) (UndoResult, error) { return t.Undo(a) },
				)
			},
		},
	}

	out := make([]tool.Tool, 0, len(defs))
	for _, d := range defs {
		tt, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("build tool %q: %w", d.name, err)
		}
		out = append(out, tt)
	}
	return out, nil
}

// toolDef is a small record so the registration table reads cleanly.
type toolDef struct {
	name        string
	description string
	build       func() (tool.Tool, error)
}

// execDescription returns the description string for the exec tool.
// When cap > 0 it appends a note stating the per-stream character limit
// and how to work around it. cap == 0 means unlimited; the note is omitted.
func execDescription(cap int) string {
	base := "Execute a shell command. Foreground (default) returns the output; background " +
		"(background=true) returns a process_id to inspect via process_status / process_kill. " +
		"WARNING: grants full shell access in the agent's sandbox."
	if cap > 0 {
		return base + fmt.Sprintf(
			" Stdout and stderr are each capped at %d chars; truncated output ends with a "+
				"[truncated: ...] marker, pipe through head/tail/grep or redirect to a file when you expect more.",
			cap,
		)
	}
	return base
}

// readFileDescription returns the description string for the read_file tool.
// When cap > 0 it appends a note stating the total character limit.
// cap == 0 means unlimited; the note is omitted.
func readFileDescription(cap int) string {
	base := "Read a file's contents. Supports reading specific line ranges (offset+limit) " +
		"to save tokens. Without ranges, reads the entire file."
	if cap > 0 {
		return base + fmt.Sprintf(
			" Output is capped at %d chars total; when truncated the result carries "+
				"truncated=true and a note, use ranges to read the remainder.",
			cap,
		)
	}
	return base
}

// processStatusDescription returns the description string for the
// process_status tool. When cap > 0 it appends a note stating the
// per-stream character limit for stored output.
// cap == 0 means unlimited; the note is omitted.
func processStatusDescription(cap int) string {
	base := "Get status and output of background processes. Without an id, lists all of them."
	if cap > 0 {
		return base + fmt.Sprintf(" Stored stdout/stderr are capped at %d chars each.", cap)
	}
	return base
}

// searchDescription returns the description string for the search tool.
// When cap > 0 it appends a note stating the total character limit on
// the result and how to narrow it. cap == 0 means unlimited; the note
// is omitted.
func searchDescription(cap int) string {
	base := "Search for text patterns in files recursively. Returns matching file paths, " +
		"line numbers, and content with configurable context."
	if cap > 0 {
		return base + fmt.Sprintf(
			" The result is capped at %d chars total across all matches; when truncated it "+
				"carries truncated=true and a note, narrow the query with a tighter pattern, "+
				"include/exclude globs, or max_results.",
			cap,
		)
	}
	return base
}
