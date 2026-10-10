package app

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/herdr/herdrtest"
	"github.com/kryptamine/herdr-auto-title/internal/namer"
	"github.com/kryptamine/herdr-auto-title/internal/reads/readstest"
)

// taskNamer answers an agent with one name and a workspace with another, and
// records what it was asked.
type taskNamer struct {
	mu        sync.Mutex
	agent     string
	workspace string
	asked     []namer.Request
}

func (n *taskNamer) String() string { return "task" }

func (n *taskNamer) Name(_ context.Context, req namer.Request) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.asked = append(n.asked, req)

	if req.Task == namer.TaskWorkspace {
		return n.workspace, nil
	}

	return n.agent, nil
}

func (n *taskNamer) workspaceRequests() []namer.Request {
	n.mu.Lock()
	defer n.mu.Unlock()

	var asked []namer.Request

	for _, req := range n.asked {
		if req.Task == namer.TaskWorkspace {
			asked = append(asked, req)
		}
	}

	return asked
}

// workspaceSession is one workspace, labelled the way Herdr labels one nobody
// named, whose one tab holds the agent pane.
func workspaceSession(label string) *herdrtest.Client {
	client := herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", WorkspaceID: "wE", Label: "1"}},
		[]herdr.PaneInfo{agentPane()},
	)
	client.SetWorkspaces(herdr.WorkspaceInfo{WorkspaceID: "wE", Label: label})

	return client
}

// startWorkspaceNaming builds a harness that names agents and workspaces
// through backend, as main wires them: one service for both.
func startWorkspaceNaming(t *testing.T, client *herdrtest.Client, backend namer.Backend) *harness {
	t.Helper()

	return startWorkspaceNamingEvery(t, client, backend, 0)
}

// startWorkspaceNamingEvery is startWorkspaceNaming with both intervals set to
// every, which a test makes tiny so that any change to the inputs is asked
// about at once; zero keeps the defaults.
func startWorkspaceNamingEvery(
	t *testing.T,
	client *herdrtest.Client,
	backend namer.Backend,
	every time.Duration,
) *harness {
	t.Helper()

	home := transcript(t, typed("fix the dashboard charts"))
	readstest.Transcript(t, home, otherSession, typed("add a legend"))

	h := startConfigured(t, client, transcriptConfig(home))
	service := namer.New(namer.Options{
		Backend:           backend,
		Interval:          every,
		WorkspaceInterval: every,
	})
	h.app.WithAgentNamer(service).WithWorkspaceNamer(service)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go service.Run(ctx)

	return h
}

func TestAWorkspaceIsNamedFromWhatIsGoingOnInIt(t *testing.T) {
	t.Parallel()

	backend := &taskNamer{agent: "dashboard-charts", workspace: "Dashboard Chart Work Today"}
	h := startWorkspaceNaming(t, workspaceSession("dashboard"), backend)

	pollUntil(t, h, func() bool { return len(h.client.WorkspaceRenames()) > 0 })

	if got := h.client.WorkspaceRenames()[0]; got.WorkspaceID != "wE" ||
		got.Label != "dashboard chart work" {
		t.Errorf("rename = %+v, want wE labelled with three words", got)
	}

	asked := backend.workspaceRequests()[0]
	if len(asked.Dirs) != 1 || len(asked.Agents) != 1 ||
		asked.Agents[0].Request != "fix the dashboard charts" {
		t.Errorf("asked with dirs %q, agents %+v", asked.Dirs, asked.Agents)
	}
}

func TestAWorkspaceWhoseContentsHoldStillIsAskedAboutOnce(t *testing.T) {
	t.Parallel()

	// Two agents, so the order the poll meets them in could show in what the
	// workspace is named from; a nanosecond interval asks about any change.
	client := herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", WorkspaceID: "wE", Label: "1"}},
		[]herdr.PaneInfo{
			agentPaneInfo("wE:p1", testSession, dashboard, true),
			agentPaneInfo("wE:p2", otherSession, dashboard, false),
		},
	)
	client.SetWorkspaces(herdr.WorkspaceInfo{WorkspaceID: "wE", Label: "dashboard"})

	backend := &taskNamer{agent: "dashboard-charts", workspace: "dashboard charts"}
	h := startWorkspaceNamingEvery(t, client, backend, time.Nanosecond)

	// Once both agents' names are on, nothing in the workspace changes again.
	pollUntil(t, h, func() bool {
		asked := backend.workspaceRequests()

		return len(asked) > 0 && !slices.ContainsFunc(asked[len(asked)-1].Agents,
			func(a namer.AgentBrief) bool { return a.Name == "" })
	})

	before := len(backend.workspaceRequests())

	h.polls(10)
	time.Sleep(20 * time.Millisecond)
	h.polls(2)

	if after := len(backend.workspaceRequests()); after != before {
		t.Errorf("asked %d more times about a workspace that did not change", after-before)
	}
}

func TestAWorkspaceTheUserLabelledIsNeverAskedAbout(t *testing.T) {
	t.Parallel()

	client := workspaceSession("dashboard")
	backend := &taskNamer{agent: "dashboard-charts", workspace: "dashboard charts"}
	h := startWorkspaceNamingEvery(t, client, backend, time.Nanosecond)

	h.poll() // the first poll, which settles what is there

	// What is in the workspace changes in the same poll that first sees the
	// user's label, which would make a new name due if the label were not the
	// user's: the lock must hold from the poll that takes it.
	client.RenameWorkspaceAsUser("wE", "Quarterly Review")
	client.SetAgentName("wE:p1", "reviewer")
	h.polls(3)
	time.Sleep(20 * time.Millisecond)
	h.polls(2)

	if len(client.WorkspaceRenames()) != 0 {
		t.Errorf("renamed %v, want the user's label kept", client.WorkspaceRenames())
	}

	for _, req := range backend.workspaceRequests() {
		if req.Current == "Quarterly Review" {
			t.Error("the model was asked about a workspace the user labelled")
		}
	}
}

func TestAWorkspaceFingerprintIgnoresLabelsButNotContents(t *testing.T) {
	t.Parallel()

	base := namer.Request{
		Task:   namer.TaskWorkspace,
		Dirs:   []string{"~/repos/a"},
		Repos:  []string{"a"},
		Agents: []namer.AgentBrief{{Name: "fix-a", Request: "fix it"}},
	}

	relabelled := base
	relabelled.Current, relabelled.Taken = "a work", []string{"b work"}

	if fingerprint(base) != fingerprint(relabelled) {
		t.Error("its own label, or the others', changed the fingerprint")
	}

	prompted := base
	prompted.Agents = []namer.AgentBrief{{Name: "fix-a", Request: "now test it"}}

	if fingerprint(base) == fingerprint(prompted) {
		t.Error("a new request to an agent left the fingerprint as it was")
	}

	if fingerprint(namer.Request{Task: namer.TaskWorkspace}) != "" {
		t.Error("an empty workspace has something to be named from")
	}
}
