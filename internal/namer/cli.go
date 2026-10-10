package namer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultCLITimeout bounds one `claude -p` call. Measured at 1.7 s for a name
// on Haiku 5.5; the margin covers a cold start and a slow network.
const DefaultCLITimeout = 60 * time.Second

// CLI asks for a name through the `claude` command line, which bills the
// subscription Claude Code is logged in with rather than an API key.
//
// The call is sealed off from the session it runs inside:
//   - --safe-mode skips hooks, plugins, MCP servers and CLAUDE.md, so the
//     user's Herdr integration hook cannot report this call as an agent.
//     --bare would skip them too but also skips the stored login (probed:
//     "Not logged in"), so it cannot be used here.
//   - --no-session-persistence writes no transcript for Auto Title to find.
//   - --tools "" gives the model nothing to run.
//   - HERDR_* is removed from the environment, and so are the API credentials,
//     which would otherwise make the CLI bill the API instead.
//   - Nothing from the terminal reaches the argument vector: the payload goes
//     to stdin, and every argument is a constant or the configured model.
type CLI struct {
	// Binary is the claude executable, looked up on PATH when not absolute.
	Binary  string
	Model   string
	Timeout time.Duration
}

func (c CLI) String() string { return "cli:" + c.Model }

// cliResult is the part of `claude -p --output-format json` that is read.
type cliResult struct {
	IsError          bool    `json:"is_error"`
	Result           string  `json:"result"`
	StructuredOutput *answer `json:"structured_output"`
}

func (c CLI) Name(ctx context.Context, req Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	//nolint:gosec // every argument is a constant or configuration; the payload goes to stdin
	cmd := exec.CommandContext(ctx, c.binary(),
		"-p",
		"--safe-mode",
		"--model", c.Model,
		"--effort", "low",
		"--tools", "",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-chrome",
		"--output-format", "json",
		"--json-schema", schemaJSON(),
		"--system-prompt", systemPrompt,
	)
	cmd.Stdin = strings.NewReader(payload(req))
	cmd.Env = sealedEnv(os.Environ())
	cmd.Dir = os.TempDir()

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("claude -p: %w: %s", err, firstLine(stderr.String()))
	}

	var res cliResult
	if jsonErr := json.Unmarshal(out, &res); jsonErr != nil {
		return "", fmt.Errorf("claude -p: unreadable output: %w", jsonErr)
	}

	if res.IsError {
		return "", fmt.Errorf("claude -p: %s", firstLine(res.Result))
	}

	if res.StructuredOutput == nil || res.StructuredOutput.Name == "" {
		return "", errors.New("claude -p: no name in the structured output")
	}

	return res.StructuredOutput.Name, nil
}

func (c CLI) binary() string {
	if c.Binary == "" {
		return "claude"
	}

	return c.Binary
}

func (c CLI) timeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultCLITimeout
	}

	return c.Timeout
}

// sealedEnv is the environment minus what would tie the call to the session
// or to a paid API: Herdr's pane variables, Claude Code's nested-session
// markers, and API credentials.
func sealedEnv(env []string) []string {
	kept := make([]string, 0, len(env))

	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")

		switch {
		case strings.HasPrefix(key, "HERDR_"):
		case key == "CLAUDECODE", key == "CLAUDE_CODE_ENTRYPOINT":
		case key == "ANTHROPIC_API_KEY", key == "ANTHROPIC_AUTH_TOKEN":
		default:
			kept = append(kept, kv)
		}
	}

	return kept
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
