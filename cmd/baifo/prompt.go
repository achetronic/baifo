// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// promptSecret reads a secret value from the terminal without echoing
// it. If stdin is not a terminal (piped input, tests) it falls back to
// a normal line read, which is the only sensible behaviour for
// non-interactive contexts.
func promptSecret(label string) (string, error) {
	fmt.Print(label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
