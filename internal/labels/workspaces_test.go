package labels_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/herdr/herdrtest"
	"github.com/kryptamine/herdr-auto-title/internal/labels"
	"github.com/kryptamine/herdr-auto-title/internal/resolver"
)

const theWorkspace = "wE"

// workspaceSession is a stubbed Herdr holding one workspace labelled label.
func workspaceSession(label string) *herdrtest.Client {
	client := herdrtest.New([]herdr.TabInfo{{TabID: theTab, Label: "1"}}, nil)
	client.SetWorkspaces(herdr.WorkspaceInfo{WorkspaceID: theWorkspace, Label: label})

	return client
}

// nameWorkspace runs one whole pass over the session, wanting its workspace
// named want, with folders as the directory names Herdr may have labelled it by.
func nameWorkspace(
	t *testing.T,
	l *labels.Labels,
	client *herdrtest.Client,
	want string,
	folders ...string,
) {
	t.Helper()

	snapshot, err := herdr.SessionSnapshot(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}

	pass := l.Pass(client, snapshot)

	for _, ws := range snapshot.Workspaces {
		pass.Workspace(context.Background(), ws.WorkspaceID, ws.Label, folders,
			resolver.Decision{Name: want})
	}

	pass.Settle()
}

func wantWorkspaceRenames(t *testing.T, client *herdrtest.Client, want ...string) {
	t.Helper()

	renames := client.WorkspaceRenames()
	if len(renames) != len(want) {
		t.Fatalf("workspace renames = %v, want %v", renames, want)
	}

	for i, rename := range renames {
		if rename.Label != want[i] {
			t.Errorf("workspace rename %d = %q, want %q", i, rename.Label, want[i])
		}
	}
}

func TestALabelPresentOnTheFirstPollIsRenamed(t *testing.T) {
	t.Parallel()

	l, client := load(""), workspaceSession("Self-Learn")

	nameWorkspace(t, l, client, "herdr agent naming", ".config")
	nameWorkspace(t, l, client, "herdr agent naming", ".config")

	wantWorkspaceRenames(t, client, "herdr agent naming")
}

func TestAWorkspaceLabelledAfterAFolderIsStillNobodys(t *testing.T) {
	t.Parallel()

	l, client := load(""), workspaceSession("dotfiles")
	nameWorkspace(t, l, client, "", "dotfiles")

	// A new workspace Herdr labelled after the folder its pane is in, seen
	// after the first poll, the way a cd would leave it — in another case too.
	client.SetWorkspaces(herdr.WorkspaceInfo{WorkspaceID: "wF", Label: "Herdr-Auto-Title"})
	nameWorkspace(t, l, client, "herdr naming", "herdr-auto-title")

	wantWorkspaceRenames(t, client, "herdr naming")
}

func TestAWorkspaceTheUserLabelledIsLeftAlone(t *testing.T) {
	t.Parallel()

	l, client := load(""), workspaceSession("dotfiles")
	nameWorkspace(t, l, client, "desktop config", "dotfiles")

	client.RenameWorkspaceAsUser(theWorkspace, "my stuff")
	nameWorkspace(t, l, client, "something else", "dotfiles")

	wantWorkspaceRenames(t, client, "desktop config")
}

func TestAWorkspaceLockSurvivesARestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "claims.json")
	client := workspaceSession("dotfiles")

	first := load(path)
	nameWorkspace(t, first, client, "desktop config", "dotfiles")
	client.RenameWorkspaceAsUser(theWorkspace, "my stuff")
	nameWorkspace(t, first, client, "desktop config", "dotfiles")

	second := load(path)
	nameWorkspace(t, second, client, "desktop config", "dotfiles")

	wantWorkspaceRenames(t, client, "desktop config")
}
