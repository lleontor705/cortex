package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/webkey"
)

// runWeb dispatches `cortex web` subcommands for the embedded web UI surface.
func runWeb(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writef(stderr, "Usage: cortex web <key> [options]\n")
		return 1
	}
	switch args[0] {
	case "key":
		return runWebKey(args[1:], stdout, stderr)
	default:
		writef(stderr, "unknown web subcommand: %s (valid: key)\n", args[0])
		return 1
	}
}

func runWebKey(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writef(stderr, "Usage: cortex web key <show|regenerate> [--key-file PATH]\n")
		return 1
	}
	switch args[0] {
	case "show":
		return runWebKeyShow(args[1:], stdout, stderr)
	case "regenerate":
		return runWebKeyRegenerate(args[1:], stdout, stderr)
	default:
		writef(stderr, "unknown web key subcommand: %s (valid: show, regenerate)\n", args[0])
		return 1
	}
}

// runWebKeyShow reports whether a web key exists and where it lives. It never
// prints the plaintext secret, which is only ever emitted by regeneration.
func runWebKeyShow(args []string, stdout, stderr io.Writer) int {
	store, err := webKeyStore(args)
	if err != nil {
		writef(stderr, "error: %v\n", err)
		return 1
	}

	exists, err := store.Exists()
	if err != nil {
		writef(stderr, "error: %v\n", err)
		return 1
	}
	if !exists {
		writef(stdout, "Web access key: not found\n")
		writef(stdout, "Key file:       %s\n", store.Path())
		writef(stdout, "A key is generated automatically on the first `cortex serve` boot.\n")
		return 0
	}

	record, err := store.Load()
	if err != nil {
		writef(stderr, "error: %v\n", err)
		return 1
	}
	writef(stdout, "Web access key: present\n")
	writef(stdout, "Key file:       %s\n", store.Path())
	writef(stdout, "Key prefix:     %s\n", record.Prefix)
	writef(stdout, "The plaintext key is never shown here; run `cortex web key regenerate` to rotate it.\n")
	return 0
}

// runWebKeyRegenerate rotates the persisted key and prints the fresh plaintext
// exactly once. Rotation is atomic, so a failure leaves the previous key valid.
func runWebKeyRegenerate(args []string, stdout, stderr io.Writer) int {
	store, err := webKeyStore(args)
	if err != nil {
		writef(stderr, "error: %v\n", err)
		return 1
	}

	secret, err := store.Regenerate()
	if err != nil {
		writef(stderr, "error: failed to regenerate web access key: %v\n", err)
		writef(stderr, "The previous key, if any, is unchanged.\n")
		return 1
	}

	writef(stdout, "Web access key regenerated.\n")
	writef(stdout, "Key file:       %s\n", store.Path())
	writef(stdout, "\nNew web access key (shown once):\n")
	writef(stdout, "%s\n", secret)
	writef(stdout, "\nStore it in a safe place; it will not be shown again.\n")
	writef(stdout, "The previous key is no longer valid.\n")
	return 0
}

// webKeyStore resolves the key file location and binds a store to it. The
// `web.key_file` config override is wired by the serve-mount task; until then the
// flag (or the documented default) is authoritative.
func webKeyStore(args []string) (*webkey.Store, error) {
	path, err := webKeyPath(args)
	if err != nil {
		return nil, err
	}
	return webkey.NewStore(path)
}

func webKeyPath(args []string) (string, error) {
	path := config.DefaultWebKeyFile()
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--key-file" || args[i] == "-k":
			if i+1 >= len(args) {
				return "", errors.New("--key-file requires a path")
			}
			path = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--key-file="):
			path = strings.TrimPrefix(args[i], "--key-file=")
		default:
			return "", fmt.Errorf("unexpected argument: %s", args[i])
		}
	}
	return path, nil
}
