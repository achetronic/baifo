// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"os/exec"
	"runtime"
	"sync"
)

// windowsShellInfo is the resolved Windows shell choice.
type windowsShellInfo struct {
	// name is the human-readable label: "pwsh" or "powershell".
	name string
	// exe is the executable passed to exec.Command.
	exe string
}

// resolveWindowsShellWith selects the best available PowerShell variant.
// It prefers "pwsh" (PowerShell 7+); if not found it falls back to
// "powershell.exe" (Windows PowerShell 5).
//
// lookPath is injected so callers (e.g. tests) can simulate LookPath results
// without a real PowerShell installation on the host.
func resolveWindowsShellWith(lookPath func(string) (string, error)) windowsShellInfo {
	if path, err := lookPath("pwsh"); err == nil {
		return windowsShellInfo{name: "pwsh", exe: path}
	}
	return windowsShellInfo{name: "powershell", exe: "powershell.exe"}
}

// resolvedWindowsShell is computed exactly once (at first use) via the real
// exec.LookPath so the path resolution cost is paid only once per process.
var resolvedWindowsShell = sync.OnceValue(func() windowsShellInfo {
	return resolveWindowsShellWith(exec.LookPath)
})

// shellCommand returns an *exec.Cmd that runs command through the appropriate
// shell for the current OS:
//
//   - non-Windows: sh -c <command>
//   - Windows:     pwsh (or powershell.exe) -NoProfile -NonInteractive -Command <command>
func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		return exec.Command("sh", "-c", command)
	}
	info := resolvedWindowsShell()
	return exec.Command(info.exe, "-NoProfile", "-NonInteractive", "-Command", command)
}

// shellName returns the human-readable name of the shell used to run commands
// on the current OS:
//   - "sh"         on non-Windows
//   - "pwsh"       on Windows when pwsh is available
//   - "powershell" on Windows when only powershell.exe is available
func shellName() string {
	if runtime.GOOS != "windows" {
		return "sh"
	}
	return resolvedWindowsShell().name
}
