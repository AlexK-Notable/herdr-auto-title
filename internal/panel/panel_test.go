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

const socket = "/tmp/panel-test-herdr.sock"

// session is a stubbed Herdr with two workspaces and a state directory in
// which nothing is running yet.
func session(t *testing.T) (panel.Env, *herdrtest.Client) {
	t.Helper()

	client := herdrtest.New([]herdr.TabInfo{{TabID: "w1:t1", Label: "1"}}, nil)
	client.SetWorkspaces(
		herdr.WorkspaceInfo{WorkspaceID: "w1", Label: "dashboard"},
		herdr.WorkspaceInfo{WorkspaceID: "w2", Label: "Quarterly Review"},
	)

	dir := t.TempDir()

	return panel.Env{
		Client:     client,
		Socket:     socket,
		StateDir:   dir,
		ManualPath: filepath.Join(dir, "manual-names.json"),
	}, client
}

// claim makes this process the instance running the session.
func claim(t *testing.T, env panel.Env) {
	t.Helper()

	_, _, err := instance.Take(
		context.Background(),
		instance.File(env.StateDir, env.Socket),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func lock(t *testing.T, env panel.Env, label string) {
	t.Helper()

	raw := `{"claimed_workspaces":{"w2":"` + label + `"}}`
	if err := os.WriteFile(env.ManualPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspacesListsEachWithWhoNamedIt(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	lock(t, env, "Quarterly Review")

	var out bytes.Buffer
	if code := panel.Workspaces(context.Background(), &out, env); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}

	want := `[{"id":"w1","label":"dashboard","named_by":"auto-title"},` +
		`{"id":"w2","label":"Quarterly Review","named_by":"you"}]` + "\n"
	if out.String() != want {
		t.Errorf("printed %s, want %s", out.String(), want)
	}
}

func TestALockWhoseLabelMovedOnIsNotTheUsers(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	lock(t, env, "An Older Label")

	var out bytes.Buffer
	panel.Workspaces(context.Background(), &out, env)

	var got []map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got[1]["named_by"] != "auto-title" {
		t.Errorf("w2 = %v, want auto-title", got[1])
	}
}

func TestWorkspacesWithoutASessionFailsWithAMessage(t *testing.T) {
	t.Parallel()

	env, client := session(t)
	client.SetCallError(os.ErrClosed)

	var out bytes.Buffer

	code := panel.Workspaces(context.Background(), &out, env)
	if code != 1 ||
		out.String() != `{"ok":false,"message":"could not read the Herdr session"}`+"\n" {
		t.Errorf("exit %d, printed %s", code, out.String())
	}
}

func act(t *testing.T, env panel.Env, op, id string) (int, string) {
	t.Helper()

	var out bytes.Buffer

	code := panel.Act(context.Background(), &out, env, op, id)

	return code, out.String()
}

func TestRenameNowHandsTheRequestToTheRunningInstance(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	claim(t, env)

	code, out := act(t, env, inbox.OpRenameNow, "w1")
	if code != 0 || out != `{"ok":true,"message":"dashboard: renaming soon"}`+"\n" {
		t.Errorf("exit %d, printed %s", code, out)
	}

	got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket))
	if len(got) != 1 || got[0] != (inbox.Request{Op: inbox.OpRenameNow, WorkspaceID: "w1"}) {
		t.Errorf("inbox held %v", got)
	}
}

func TestReleaseHandsAWorkspaceBack(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	claim(t, env)
	lock(t, env, "Quarterly Review")

	code, out := act(t, env, inbox.OpRelease, "w2")
	want := `{"ok":true,"message":"Quarterly Review: handed back to Auto Title"}` + "\n"

	if code != 0 || out != want {
		t.Errorf("exit %d, printed %s", code, out)
	}

	if got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket)); len(got) != 1 {
		t.Errorf("inbox held %v", got)
	}
}

func TestRenameNowRefusesAWorkspaceTheUserNamed(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	claim(t, env)
	lock(t, env, "Quarterly Review")

	code, out := act(t, env, inbox.OpRenameNow, "w2")
	want := `{"ok":false,"message":"Quarterly Review was named by you; hand it back first"}` + "\n"

	if code != 1 || out != want {
		t.Errorf("exit %d, printed %s", code, out)
	}

	if got := inbox.Drain(instance.Inbox(env.StateDir, env.Socket)); len(got) != 0 {
		t.Errorf("a refused request was sent: %v", got)
	}
}

func TestAnUnknownWorkspaceIsRefused(t *testing.T) {
	t.Parallel()

	env, _ := session(t)
	claim(t, env)

	code, out := act(t, env, inbox.OpRelease, "w9")
	if code != 1 || out != `{"ok":false,"message":"no workspace w9"}`+"\n" {
		t.Errorf("exit %d, printed %s", code, out)
	}
}

func TestNothingIsSentWhenNoInstanceIsRunning(t *testing.T) {
	t.Parallel()

	env, _ := session(t)

	code, out := act(t, env, inbox.OpRenameNow, "w1")
	want := `{"ok":false,"message":"Auto Title is not running in this session"}` + "\n"

	if code != 1 || out != want {
		t.Errorf("exit %d, printed %s", code, out)
	}

	if _, err := os.Stat(instance.Inbox(env.StateDir, env.Socket)); err == nil {
		t.Error("an inbox was made for a session nobody serves")
	}
}
