package inbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kryptamine/herdr-auto-title/internal/inbox"
	"github.com/kryptamine/herdr-auto-title/internal/instance"
)

func TestRequestsComeOutOldestFirstAndAreRemoved(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "box")
	sent := []inbox.Request{
		{Op: inbox.OpRenameNow, WorkspaceID: "w1"},
		{Op: inbox.OpRelease, WorkspaceID: "w2"},
		{Op: inbox.OpRenameNow, WorkspaceID: "w3"},
	}

	for _, req := range sent {
		if err := inbox.Send(dir, req); err != nil {
			t.Fatal(err)
		}
	}

	got := inbox.Drain(dir)
	if len(got) != len(sent) {
		t.Fatalf("drained %v, want %v", got, sent)
	}

	for i := range sent {
		if got[i] != sent[i] {
			t.Errorf("request %d = %+v, want %+v", i, got[i], sent[i])
		}
	}

	if again := inbox.Drain(dir); len(again) != 0 {
		t.Errorf("a second drain returned %v", again)
	}

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("%d files left behind", len(left))
	}
}

func TestASentRequestIsPrivateAndLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "box")
	if err := inbox.Send(dir, inbox.Request{Op: inbox.OpRelease, WorkspaceID: "w1"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, err = %v; want exactly the request", entries, err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 && filepath.Separator == '/' {
		t.Errorf("directory mode = %o, want owner only", mode)
	}
}

func TestAFileThatIsNotARequestIsRemovedAndTheRestStillServed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	junk := map[string]string{
		"1-a.json": "{not json",
		"2-b.json": `{"op":"explode","workspace_id":"w1"}`,
		"3-c.json": `{"op":"release","workspace_id":""}`,
		"4-d.json": string(make([]byte, 10000)),
	}

	for name, body := range junk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := inbox.Send(dir, inbox.Request{Op: inbox.OpRelease, WorkspaceID: "w9"}); err != nil {
		t.Fatal(err)
	}

	got := inbox.Drain(dir)
	if len(got) != 1 || got[0].WorkspaceID != "w9" {
		t.Errorf("drained %v, want only the real request", got)
	}

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("%d malformed files left behind", len(left))
	}
}

func TestADirectoryThatDoesNotExistHoldsNothing(t *testing.T) {
	t.Parallel()

	if got := inbox.Drain(filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Errorf("drained %v from nowhere", got)
	}
}

func TestTwoSessionsDoNotShareAnInbox(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	one := instance.Inbox(state, "/run/user/1000/herdr-a.sock")
	two := instance.Inbox(state, "/run/user/1000/herdr-b.sock")

	if one == two || one == "" {
		t.Fatalf("inboxes %q and %q should differ", one, two)
	}

	if err := inbox.Send(one, inbox.Request{Op: inbox.OpRelease, WorkspaceID: "w1"}); err != nil {
		t.Fatal(err)
	}

	if got := inbox.Drain(two); len(got) != 0 {
		t.Errorf("the other session received %v", got)
	}

	if got := inbox.Drain(one); len(got) != 1 {
		t.Errorf("its own session received %v", got)
	}

	if instance.Inbox("", "/x") != "" {
		t.Error("no state directory should mean no inbox")
	}
}
