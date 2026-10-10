package namer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// keyTimeout bounds the command that prints the API key: a secrets manager
// may go to the network, but a key that takes longer than this is not coming.
const keyTimeout = 20 * time.Second

// KeyFromCommand runs argv and returns what it prints, trimmed, as the API key.
// argv is configuration and runs without a shell, so quoting and pipes are not
// interpreted: point it at a script. The key is never logged, and neither is
// the command's output when it fails — only its exit status.
func KeyFromCommand(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no key command configured")
	}

	ctx, cancel := context.WithTimeout(ctx, keyTimeout)
	defer cancel()

	//nolint:gosec // argv is the user's own configuration, never terminal-derived
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = sealedKeyEnv(cmd.Environ())

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("key command %s failed: %w", argv[0], err)
	}

	key := strings.TrimSpace(stdout.String())
	if key == "" || strings.ContainsAny(key, " \n\t") {
		return "", fmt.Errorf("key command %s printed no single key", argv[0])
	}

	return key, nil
}

// sealedKeyEnv drops Herdr's pane variables, which the key command has no use
// for, and keeps everything a secrets manager may need.
func sealedKeyEnv(env []string) []string {
	kept := make([]string, 0, len(env))

	for _, kv := range env {
		if !strings.HasPrefix(kv, "HERDR_") {
			kept = append(kept, kv)
		}
	}

	return kept
}
