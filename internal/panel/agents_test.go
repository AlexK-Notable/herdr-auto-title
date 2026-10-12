package panel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/herdr/herdrtest"
	"github.com/kryptamine/herdr-auto-title/internal/inbox"
	"github.com/kryptamine/herdr-auto-title/internal/instance"
	"github.com/kryptamine/herdr-auto-title/internal/panel"
)

// agentSession is a stubbed Herdr whose workspace "dashboard" holds a Claude
// agent Auto Title named (p1), one the user named (p2, locked), a Codex agent
// (p3), which Auto Title leaves alone, and a Claude agent with no name (p4).
func agentSession(t *testing.T) panel.Env {
	t.Helper()

	pane := func(id, kind string) herdr.PaneInfo {
		return herdr.PaneInfo{PaneID: id, TabID: "w1:t1", Agent: kind}
	}

	client := herdrtest.New(
		[]herdr.TabInfo{{TabID: "w1:t1", WorkspaceID: "w1", Label: "1"}},
		[]herdr.PaneInfo{
			pane("w1:p1", "claude"),
			pane("w1:p2", "claude"),
			pane("w1:p3", "codex"),
			pane("w1:p4", "claude"),
		},
	)
	client.SetWorkspaces(herdr.WorkspaceInfo{WorkspaceID: "w1", Label: "dashboard"})
	client.SetAgentName("w1:p1", "dashboard-charts")
	client.SetAgentName("w1:p2", "reviewer")
	client.SetAgentName("w1:p3", "helper")

	dir := t.TempDir()
	env := panel.Env{
		Client:     client,
		Socket:     socket,
		StateDir:   dir,
		ManualPath: filepath.Join(dir, "manual-names.json"),
	}

	raw := `{"locked_tabs":{},"locked_panes":{},"locked_agents":{"w1:p2":"reviewer"}}`
	if err := os.WriteFile(env.ManualPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	return env
}

func actAgent(t *testing.T, env panel.Env, op, id string) (int, string) {
	t.Helper()

	var out bytes.Buffer

	code := panel.ActAgent(context.Background(), &out, env, op, id)

	return code, out.String()
}

func TestAgentsListsClaudeAgentsWithWhoNamedThem(t *testing.T) {
	t.Parallel()

	env := agentSession(t)

	var out bytes.Buffer
	if code := panel.Agents(context.Background(), &out, env); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}

	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}

	want := []map[string]string{
		{
			"pane_id":  "w1:p1",
			"name":     "dashboard-charts",
			"label":    "dashboard-charts",
			"where":    "dashboard",
			"named_by": "auto-title",
		},
		{"pane_id": "w1:p2", "name": "reviewer", "where": "dashboard", "named_by": "you"},
		{
			"pane_id":  "w1:p4",
			"name":     "",
			"label":    "unnamed (w1:p4)",
			"where":    "dashboard",
			"named_by": "",
		},
	}

	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}

	for i := range want {
		for k, v := range want[i] {
			if rows[i][k] != v {
				t.Errorf("row %d %s = %q, want %q", i, k, rows[i][k], v)
			}
		}
	}
}

func TestAgentRenameNowHandsTheRequestToTheRunningInstance(t *testing.T) {
	t.Parallel()

	env := agentSession(t)
	claim(t, env)

	code, out := actAgent(t, env, inbox.OpAgentRenameNow, "w1:p1")
	want := `{"ok":true,"message":"dashboard-charts: asking for a name (it keeps this one if it still fits)"}` + "\n"

	if code != 0 || out != want {
		t.Errorf("exit %d, printed %s", code, out)
	}

	got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket))
	if len(got) != 1 || got[0] != (inbox.Request{Op: inbox.OpAgentRenameNow, PaneID: "w1:p1"}) {
		t.Errorf("inbox held %v", got)
	}
}

func TestAgentRenameNowRefusesANameTheUserGave(t *testing.T) {
	t.Parallel()

	env := agentSession(t)
	claim(t, env)

	code, out := actAgent(t, env, inbox.OpAgentRenameNow, "w1:p2")
	want := `{"ok":false,"message":"reviewer was named by you; hand it back first"}` + "\n"

	if code != 1 || out != want {
		t.Errorf("exit %d, printed %s", code, out)
	}

	if got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket)); len(got) != 0 {
		t.Errorf("a refused request was sent: %v", got)
	}
}

func TestAgentReleaseHandsANameBack(t *testing.T) {
	t.Parallel()

	env := agentSession(t)
	claim(t, env)

	code, out := actAgent(t, env, inbox.OpAgentRelease, "w1:p2")
	if code != 0 || out != `{"ok":true,"message":"reviewer: handed back to Auto Title"}`+"\n" {
		t.Errorf("exit %d, printed %s", code, out)
	}

	got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket))
	if len(got) != 1 || got[0] != (inbox.Request{Op: inbox.OpAgentRelease, PaneID: "w1:p2"}) {
		t.Errorf("inbox held %v", got)
	}
}

func TestAnAgentAutoTitleDoesNotNameIsRefused(t *testing.T) {
	t.Parallel()

	env := agentSession(t)
	claim(t, env)

	for _, id := range []string{"w1:p3", "w1:p9"} { // Codex, and no pane at all
		code, out := actAgent(t, env, inbox.OpAgentRenameNow, id)
		if code != 1 || out != `{"ok":false,"message":"no Claude Code agent in pane `+id+`"}`+"\n" {
			t.Errorf("%s: exit %d, printed %s", id, code, out)
		}
	}

	if got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket)); len(got) != 0 {
		t.Errorf("a refused request was sent: %v", got)
	}
}

func TestNoAgentRequestIsSentWhenNoInstanceIsRunning(t *testing.T) {
	t.Parallel()

	env := agentSession(t)

	code, out := actAgent(t, env, inbox.OpAgentRenameNow, "w1:p1")
	if code != 1 ||
		out != `{"ok":false,"message":"Auto Title is not running in this session"}`+"\n" {
		t.Errorf("exit %d, printed %s", code, out)
	}
}
