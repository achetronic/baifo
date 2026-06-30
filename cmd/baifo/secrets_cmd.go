// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/secrets"
)

// runSubcommand dispatches `baifo <cmd> [args...]` to its handler.
func runSubcommand(dir string, args []string) exitCode {
	switch args[0] {
	case "secrets":
		return runSecretsCommand(dir, args[1:])
	case "server":
		return runServerCommand(dir, args[1:])
	case "chat":
		return runChatCommand(dir, args[1:])
	case "provider":
		return runProviderCommand(dir, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "baifo: unknown command %q\n", args[0])
		return exitError
	}
}

// runSecretsCommand handles `baifo secrets <verb> ...`.
func runSecretsCommand(dir string, args []string) exitCode {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: baifo secrets {set|unset|list|rotate|show-block} ...")
		return exitError
	}

	store, err := openSecretsStore(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}

	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		return secretsList(store)
	case "set":
		return secretsSet(store, rest, false)
	case "rotate":
		return secretsSet(store, rest, true)
	case "unset":
		return secretsUnset(store, rest)
	case "show-block":
		return secretsShowBlock(store)
	default:
		fmt.Fprintf(os.Stderr, "baifo: unknown secrets verb %q\n", verb)
		return exitError
	}
}

// openSecretsStore reads baifo.yaml to extract the encryption key and
// initialises the secret store. The store works with or without an
// encryption key (plaintext mode when empty), so this helper no longer
// rejects the missing-key case; the store itself decides the mode.
func openSecretsStore(dir string) (*secrets.Store, error) {
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	store, err := secrets.NewStore(dir, cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("open secrets store: %w", err)
	}
	return store, nil
}

func secretsList(store *secrets.Store) exitCode {
	entries, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	if len(entries) == 0 {
		fmt.Println("No secrets stored.")
		return exitOK
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tROTATED")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, e.Description, e.RotatedAt.Format("2006-01-02"))
	}
	_ = w.Flush()
	return exitOK
}

func secretsSet(store *secrets.Store, args []string, rotate bool) exitCode {
	if len(args) < 1 {
		verb := "set"
		if rotate {
			verb = "rotate"
		}
		fmt.Fprintf(os.Stderr, "Usage: baifo secrets %s <name> [--description=...]\n", verb)
		return exitError
	}
	name := args[0]

	value, err := promptSecret(fmt.Sprintf("Value for %q: ", name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	desc := extractDescriptionFlag(args[1:])

	if err := store.Set(name, value, desc); err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	if rotate {
		fmt.Printf("Rotated %q.\n", name)
	} else {
		fmt.Printf("Saved %q.\n", name)
	}
	return exitOK
}

func secretsUnset(store *secrets.Store, args []string) exitCode {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: baifo secrets unset <name>")
		return exitError
	}
	name := args[0]
	if err := store.Delete(name); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "baifo: no secret named %q\n", name)
			return exitError
		}
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	fmt.Printf("Removed %q.\n", name)
	return exitOK
}

// secretsShowBlock prints the system-prompt fragment listing the
// available secrets, exactly as agents would see it. Useful for
// debugging allowlists and descriptions. The values themselves are
// never printed.
func secretsShowBlock(store *secrets.Store) exitCode {
	entries, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	if len(entries) == 0 {
		fmt.Println("(no secrets - the block would be omitted from the prompt)")
		return exitOK
	}
	fmt.Println("You have access to the following secrets, referenced by name only:")
	for _, e := range entries {
		if e.Description != "" {
			fmt.Printf("- %s - %s\n", e.Name, e.Description)
			continue
		}
		fmt.Printf("- %s\n", e.Name)
	}
	return exitOK
}

// extractDescriptionFlag pulls --description=VALUE out of the trailing
// args. It is intentionally minimal: the flag package does not play
// well with positional-then-flag style we want for secrets verbs.
func extractDescriptionFlag(args []string) string {
	const prefix = "--description="
	for _, a := range args {
		if len(a) > len(prefix) && a[:len(prefix)] == prefix {
			return a[len(prefix):]
		}
	}
	return ""
}
