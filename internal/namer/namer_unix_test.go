//go:build !windows

package namer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests stand in for `claude` and a key command with shell scripts,
// which Windows cannot run.

// fakeClaude writes a script standing in for `claude` that records its argv,
// stdin and environment, and answers with reply.
func fakeClaude(t *testing.T, reply string, exit int) (bin, dir string) {
	t.Helper()

	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + dir + "/argv\"\n" +
		"cat > \"" + dir + "/stdin\"\n" +
		"env > \"" + dir + "/env\"\n" +
		"cat <<'JSON'\n" + reply + "\nJSON\n" +
		"exit " + string(rune('0'+exit)) + "\n"

	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return bin, dir
}

func read(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestTheCLIBackendIsSealedOffFromTheSession(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("KEEP_ME", "yes")

	bin, dir := fakeClaude(
		t,
		`{"is_error":false,"result":"","structured_output":{"name":"Fix Search"}}`,
		0,
	)
	cli := CLI{Binary: bin, Model: DefaultModel}

	name, err := cli.Name(
		context.Background(),
		Request{Agent: "claude", Prompts: []string{"secret prompt text"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if name != "Fix Search" {
		t.Errorf("name = %q", name)
	}

	argv := read(t, filepath.Join(dir, "argv"))
	for _, want := range []string{"-p", "--safe-mode", "--no-session-persistence", "--json-schema", DefaultModel} {
		if !strings.Contains(argv, want+"\n") {
			t.Errorf("argv lacks %q:\n%s", want, argv)
		}
	}

	if strings.Contains(argv, "secret prompt text") {
		t.Error("the payload reached the argument vector")
	}

	if !strings.Contains(read(t, filepath.Join(dir, "stdin")), "secret prompt text") {
		t.Error("the payload did not reach stdin")
	}

	env := read(t, filepath.Join(dir, "env"))
	for _, gone := range []string{"HERDR_ENV=", "HERDR_PANE_ID=", "ANTHROPIC_API_KEY=", "CLAUDECODE="} {
		if strings.Contains(env, gone) {
			t.Errorf("%s leaked into the CLI's environment", gone)
		}
	}

	if !strings.Contains(env, "KEEP_ME=yes") {
		t.Error("an unrelated variable was dropped")
	}
}

func TestTheCLIBackendReportsAnErrorResult(t *testing.T) {
	t.Parallel()

	bin, _ := fakeClaude(t, `{"is_error":true,"result":"Not logged in · Please run /login"}`, 1)

	_, err := CLI{Binary: bin, Model: DefaultModel}.Name(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("err = %v, want the CLI's own message", err)
	}
}

func TestTheKeyCommandPrintsTheKeyAndNothingElseIsAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")

	if err := os.WriteFile(good, []byte("#!/bin/sh\necho '  sk-ant-123  '\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho 'two words'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	key, err := KeyFromCommand(context.Background(), []string{good})
	if err != nil || key != "sk-ant-123" {
		t.Errorf("key = %q, err = %v", key, err)
	}

	if _, err := KeyFromCommand(context.Background(), []string{bad}); err == nil {
		t.Error("a key with a space was accepted")
	} else if strings.Contains(err.Error(), "two words") {
		t.Error("the command's output reached the error")
	}
}
