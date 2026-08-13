// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Shell selection: pure logic tests, no real process spawning.
// These exercise resolveWindowsShellWith via the injectable lookPath func.
// ---------------------------------------------------------------------------

func TestResolveWindowsShell_PwshAvailable(t *testing.T) {
	info := resolveWindowsShellWith(func(name string) (string, error) {
		if name == "pwsh" {
			return `/usr/bin/pwsh`, nil
		}
		return "", errors.New("not found")
	})
	if info.name != "pwsh" {
		t.Errorf("name: got %q, want %q", info.name, "pwsh")
	}
	if info.exe != `/usr/bin/pwsh` {
		t.Errorf("exe: got %q, want %q", info.exe, `/usr/bin/pwsh`)
	}
}

func TestResolveWindowsShell_FallbackToPowerShell(t *testing.T) {
	info := resolveWindowsShellWith(func(_ string) (string, error) {
		return "", errors.New("not found")
	})
	if info.name != "powershell" {
		t.Errorf("name: got %q, want %q", info.name, "powershell")
	}
	if info.exe != "powershell.exe" {
		t.Errorf("exe: got %q, want %q", info.exe, "powershell.exe")
	}
}

// shellCommandArgs builds a Cmd and returns the resolved executable name (base)
// plus the argument list, for assertions without running anything.
func shellCommandArgs(command string) (exe string, args []string) {
	cmd := shellCommand(command)
	// cmd.Path is the resolved exe; cmd.Args[0] is the same on real platforms.
	return cmd.Path, cmd.Args[1:] // Args[0] == cmd.Path
}

func TestShellCommand_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows path not exercised on Windows")
	}
	_, args := shellCommandArgs("echo hello")
	// Must be: sh  -c  <command>
	if len(args) != 2 {
		t.Fatalf("args: got %v, want [sh -c <cmd>] (len 2 after exe)", args)
	}
	if args[0] != "-c" {
		t.Errorf("args[0]: got %q, want -c", args[0])
	}
	if args[1] != "echo hello" {
		t.Errorf("args[1]: got %q, want %q", args[1], "echo hello")
	}
}

func TestShellName_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows shell name not exercised on Windows")
	}
	if got := shellName(); got != "sh" {
		t.Errorf("shellName: got %q, want %q", got, "sh")
	}
}

// ---------------------------------------------------------------------------
// Env merge: runs a real trivial command via processStore.Exec.
// Asserts that both the injected custom var AND an inherited var (PATH) survive.
//
// Canary: this test WILL FAIL if someone reverts the merge to the old
//
//	cmd.Env = env
//
// replacement, because PATH will vanish and the sh command itself won't run.
// ---------------------------------------------------------------------------

func TestExecEnvMerge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("env merge canary uses POSIX sh; skipped on Windows")
	}

	ps := newProcessStore()
	const customKey = "BAIFO_EXEC_TEST_VAR"
	const customVal = "hello_from_test"

	// Inject a custom variable.  If cmd.Env = env (old behaviour) were used
	// instead of append(os.Environ(), env...), the PATH would be missing and
	// the sh command itself would fail to start.
	stdout, _, exitCode, err := ps.Exec(
		// Print both the custom var and PATH so we can assert both survive.
		`printf '%s\n%s\n' "$`+customKey+`" "$PATH"`,
		"",
		[]string{customKey + "=" + customVal},
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode: got %d, want 0", exitCode)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 output lines, got: %q", stdout)
	}

	// First line: the custom variable value.
	if lines[0] != customVal {
		t.Errorf("custom var: got %q, want %q", lines[0], customVal)
	}

	// Second line: PATH must be non-empty (inherited from parent).
	if lines[1] == "" {
		t.Error("PATH was empty; env merge did not inherit parent environment; " +
			"check that cmd.Env = append(os.Environ(), env...) is used, not cmd.Env = env")
	}

	// Also cross-check against what os.Getenv sees in this process.
	hostPATH := os.Getenv("PATH")
	if hostPATH != "" && lines[1] != hostPATH {
		// The caller-supplied env did not include PATH, so the child's PATH must
		// exactly equal the parent's PATH (no override was requested).
		t.Errorf("PATH in child %q != host PATH %q", lines[1], hostPATH)
	}
}

// ---------------------------------------------------------------------------
// execDescription: unit tests for the description helper.
// ---------------------------------------------------------------------------

func TestExecDescription_ContainsShellAndGOOS(t *testing.T) {
	desc := execDescription(0)
	wantShell := shellName()
	wantOS := runtime.GOOS
	if !strings.Contains(desc, wantShell) {
		t.Errorf("execDescription(0) missing shell %q: %q", wantShell, desc)
	}
	if !strings.Contains(desc, wantOS) {
		t.Errorf("execDescription(0) missing GOOS %q: %q", wantOS, desc)
	}
}

func TestExecDescription_CapNotePresent(t *testing.T) {
	desc := execDescription(4242)
	if !strings.Contains(desc, "4242") {
		t.Errorf("execDescription(4242) missing cap value: %q", desc)
	}
	if !strings.Contains(desc, "[truncated:") {
		t.Errorf("execDescription(4242) missing truncation marker hint: %q", desc)
	}
}

func TestExecDescription_NoCappedWhenUnlimited(t *testing.T) {
	desc := execDescription(0)
	if strings.Contains(desc, "capped") {
		t.Errorf("execDescription(0) must not mention 'capped': %q", desc)
	}
}
