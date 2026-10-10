package namer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWordsKeepsAtMostThreeLowercaseWords(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"Herdr Naming":                       "herdr naming",
		"herdr-auto-title agent naming work": "herdr auto title",
		"  Desktop   config!  ":              "desktop config",
		"zNote":                              "znote",
		"???":                                "",
		"supercalifragilistic expialidocious everything": "supercalifragilistic expialidocious",
	} {
		got := Words(raw)
		if got != want {
			t.Errorf("Words(%q) = %q, want %q", raw, got, want)
		}

		if len(strings.Fields(got)) > MaxWorkspaceWords || len(got) > MaxWorkspaceName {
			t.Errorf("Words(%q) = %q, over the bounds", raw, got)
		}
	}
}

func TestAWorkspaceIsAskedAboutWithEverythingInIt(t *testing.T) {
	t.Parallel()

	req := Request{
		Task:    TaskWorkspace,
		Dirs:    []string{"~/.config", "~/repos/herdr-auto-title"},
		Repos:   []string{"herdr-auto-title"},
		Current: "herdr naming",
		Taken:   []string{"desktop config"},
		Agents: []AgentBrief{
			{Name: "herdr-agent-naming", Request: "name the workspaces too"},
			{Request: ""},
		},
	}

	got := payload(req)
	for _, want := range []string{
		"~/.config, ~/repos/herdr-auto-title",
		"Repositories: herdr-auto-title",
		"Current name (keep it if it still fits): herdr naming",
		"Taken names (do not use): desktop config",
		"- herdr-agent-naming: name the workspaces too",
		"- (unnamed): (nothing yet)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %q:\n%s", want, got)
		}
	}

	if system(req) != workspacePrompt || Clean(req, "A B C D") != "a b c" {
		t.Error("a workspace request is not asked and cleaned as one")
	}
}

func TestAWorkspaceNameComesBackAsWords(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"Herdr Agent Naming Plugin"}}
	s, _ := newService(b, "")

	s.Name("ws:wE", "fp1", Request{Task: TaskWorkspace})
	drain(s)

	if got := s.Remembered("ws:wE"); got != "herdr agent naming" {
		t.Errorf("got %q, want three words", got)
	}
}

func TestAWorkspaceIsRenamedNoMoreOftenThanItsOwnInterval(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"first", "second"}}
	s, c := newService(b, "")

	s.Name("ws:wE", "fp1", Request{Task: TaskWorkspace})
	drain(s)

	// Past the agent interval, short of the workspace one.
	c.t = c.t.Add(3 * time.Minute)

	s.Name("ws:wE", "fp2", Request{Task: TaskWorkspace})
	drain(s)

	if b.count() != 1 {
		t.Fatalf("renamed inside the workspace interval: calls = %d", b.count())
	}

	c.t = c.t.Add(3 * time.Minute)

	s.Name("ws:wE", "fp2", Request{Task: TaskWorkspace})
	drain(s)

	if b.count() != 2 {
		t.Errorf("calls = %d, want 2 once the interval passed", b.count())
	}
}

func TestANameSavedWithATurnCountIsNotAskedAboutAgain(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent-names.json")
	stored := `{"` + sess + `":{"name":"fix-search","turns":3,"at":"2026-10-10T11:00:00Z"}}`

	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	b := &fakeBackend{names: []string{"other"}}
	s, _ := newService(b, path)

	s.Name(sess, "3", Request{})
	drain(s)

	if b.count() != 0 || s.Remembered(sess) != "fix-search" {
		t.Errorf(
			"calls = %d, name %q: an unchanged session was asked about",
			b.count(),
			s.Remembered(sess),
		)
	}
}
