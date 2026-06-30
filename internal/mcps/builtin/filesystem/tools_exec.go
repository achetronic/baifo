// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// execOutputHint is appended to the truncation marker for exec/process_status
// stdout and stderr so the model knows how to recover.
const execOutputHint = "; re-run with the output piped through head/tail/grep, or redirect to a file and read it with read_file ranges"

// exec

// ExecArgs is the input of the Exec tool.
type ExecArgs struct {
	Command    string            `json:"command"`
	Workdir    string            `json:"workdir,omitempty"`
	Timeout    int               `json:"timeout,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Background bool              `json:"background,omitempty"`
}

// ExecResult is what the LLM receives. ProcessID is set only when
// Background=true; the other fields apply to the synchronous case.
type ExecResult struct {
	ProcessID       string `json:"process_id,omitempty"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

// Exec runs a shell command in the foreground (with a timeout) or in
// the background (returning a process_id that subsequent tools can
// inspect). Workdir is sanitised through resolvePath if set.
func (t *Tools) Exec(args ExecArgs) (ExecResult, error) {
	if args.Command == "" {
		return ExecResult{}, fmt.Errorf("command is required")
	}

	workdir := ""
	if args.Workdir != "" {
		abs, err := resolvePath(args.Workdir)
		if err != nil {
			return ExecResult{}, err
		}
		workdir = abs
	}

	timeout := time.Duration(args.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var env []string
	if len(args.Env) > 0 {
		env = os.Environ()
		for k, v := range args.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if args.Background {
		id, err := t.processes.Start(args.Command, workdir, env)
		if err != nil {
			return ExecResult{}, err
		}
		return ExecResult{ProcessID: id}, nil
	}

	stdout, stderr, exitCode, err := t.processes.Exec(args.Command, workdir, env, timeout)
	stdout, stdoutTrunc := truncateString(stdout, t.maxExecOutputChars, execOutputHint)
	stderr, stderrTrunc := truncateString(stderr, t.maxExecOutputChars, execOutputHint)
	if err != nil {
		return ExecResult{
			ExitCode:        -1,
			Stdout:          stdout,
			Stderr:          stderr,
			StdoutTruncated: stdoutTrunc,
			StderrTruncated: stderrTrunc,
		}, fmt.Errorf("command failed: %w", err)
	}
	return ExecResult{
		ExitCode:        exitCode,
		Stdout:          stdout,
		Stderr:          stderr,
		StdoutTruncated: stdoutTrunc,
		StderrTruncated: stderrTrunc,
	}, nil
}

// resolveWorkdirFallback is kept for parity with the original
// implementation; not used directly but documents the contract.
func resolveWorkdirFallback(workdir string) string {
	if workdir != "" {
		return workdir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return string(filepath.Separator)
	}
	return cwd
}

// process_status

// ProcessStatusArgs is the input of the ProcessStatus tool. An empty
// ID lists every background process.
type ProcessStatusArgs struct {
	ID string `json:"id,omitempty"`
}

// ProcessEntry is a snapshot of one background process.
type ProcessEntry struct {
	ID              string `json:"id"`
	Command         string `json:"command"`
	WorkDir         string `json:"workdir,omitempty"`
	StartedAt       string `json:"started_at"`
	Done            bool   `json:"done"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

// ProcessStatusResult is what the LLM receives.
type ProcessStatusResult struct {
	Processes []ProcessEntry `json:"processes"`
	Total     int            `json:"total"`
}

// ProcessStatus returns the status of one or every background process.
func (t *Tools) ProcessStatus(args ProcessStatusArgs) (ProcessStatusResult, error) {
	if args.ID == "" {
		procs := t.processes.List()
		out := make([]ProcessEntry, 0, len(procs))
		for _, p := range procs {
			out = append(out, ProcessEntry{
				ID:        p.ID,
				Command:   p.Command,
				WorkDir:   p.WorkDir,
				StartedAt: p.StartedAt.Format("2006-01-02 15:04:05"),
				Done:      p.Done,
				ExitCode:  p.ExitCode,
			})
		}
		return ProcessStatusResult{Processes: out, Total: len(out)}, nil
	}

	info, stdout, stderr, err := t.processes.Status(args.ID)
	if err != nil {
		return ProcessStatusResult{}, err
	}
	stdout, stdoutTrunc := truncateString(stdout, t.maxExecOutputChars, execOutputHint)
	stderr, stderrTrunc := truncateString(stderr, t.maxExecOutputChars, execOutputHint)
	return ProcessStatusResult{
		Processes: []ProcessEntry{{
			ID:              info.ID,
			Command:         info.Command,
			WorkDir:         info.WorkDir,
			StartedAt:       info.StartedAt.Format("2006-01-02 15:04:05"),
			Done:            info.Done,
			ExitCode:        info.ExitCode,
			Stdout:          stdout,
			Stderr:          stderr,
			StdoutTruncated: stdoutTrunc,
			StderrTruncated: stderrTrunc,
		}},
		Total: 1,
	}, nil
}

// process_kill

// ProcessKillArgs is the input of the ProcessKill tool.
type ProcessKillArgs struct {
	ID string `json:"id"`
}

// ProcessKillResult is what the LLM receives.
type ProcessKillResult struct {
	ID string `json:"id"`
}

// ProcessKill terminates a background process. The OS Kill signal is
// sent; the process may take a moment to actually exit.
func (t *Tools) ProcessKill(args ProcessKillArgs) (ProcessKillResult, error) {
	if args.ID == "" {
		return ProcessKillResult{}, fmt.Errorf("id is required")
	}
	if err := t.processes.Kill(args.ID); err != nil {
		return ProcessKillResult{}, err
	}
	return ProcessKillResult{ID: args.ID}, nil
}

// scratch

// Supported actions for ScratchArgs.Action.
const (
	ScratchSet    = "set"
	ScratchGet    = "get"
	ScratchDelete = "delete"
	ScratchList   = "list"
)

// ScratchArgs is the input of the Scratch tool. Key/Value semantics
// depend on Action; documented per case below.
type ScratchArgs struct {
	Action string `json:"action"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

// ScratchResult is what the LLM receives. Different fields are filled
// depending on the action.
type ScratchResult struct {
	Value  string            `json:"value,omitempty"`
	Items  map[string]string `json:"items,omitempty"`
	Action string            `json:"action"`
	Key    string            `json:"key,omitempty"`
}

// Scratch is a multiplexed tool for the four actions of a small KV
// store. The original filesystem-mcp shipped it as one tool with an
// `action` discriminator; we keep that shape for compatibility.
func (t *Tools) Scratch(args ScratchArgs) (ScratchResult, error) {
	switch args.Action {
	case ScratchSet:
		if args.Key == "" {
			return ScratchResult{}, fmt.Errorf("key is required for set")
		}
		t.scratch.Set(args.Key, args.Value)
		return ScratchResult{Action: ScratchSet, Key: args.Key}, nil

	case ScratchGet:
		if args.Key == "" {
			return ScratchResult{}, fmt.Errorf("key is required for get")
		}
		v, err := t.scratch.Get(args.Key)
		if err != nil {
			return ScratchResult{}, err
		}
		return ScratchResult{Action: ScratchGet, Key: args.Key, Value: v}, nil

	case ScratchDelete:
		if args.Key == "" {
			return ScratchResult{}, fmt.Errorf("key is required for delete")
		}
		t.scratch.Delete(args.Key)
		return ScratchResult{Action: ScratchDelete, Key: args.Key}, nil

	case ScratchList:
		return ScratchResult{Action: ScratchList, Items: t.scratch.List()}, nil

	default:
		return ScratchResult{}, fmt.Errorf("unknown action %q (valid: set, get, delete, list)", args.Action)
	}
}
