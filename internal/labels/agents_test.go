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

const agentPane = "wE:p1"

// agentSession is a stubbed Herdr holding one tab whose pane runs claude.
func agentSession() *herdrtest.Client {
	return herdrtest.New(
		[]herdr.TabInfo{{TabID: theTab, Label: "1"}},
		[]herdr.PaneInfo{{PaneID: agentPane, TabID: theTab, Agent: "claude"}},
	)
}

// nameAgent runs one whole pass over the session, wanting its agent named want.
func nameAgent(t *testing.T, l *labels.Labels, client *herdrtest.Client, want string) {
	t.Helper()

	snapshot, err := herdr.SessionSnapshot(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}

	pass := l.Pass(client, snapshot)

	for _, agent := range snapshot.Agents {
		pass.Agent(context.Background(), agent.PaneID, agent.Name, resolver.Decision{Name: want})
	}

	pass.Settle()
}

func wantAgentRenames(t *testing.T, client *herdrtest.Client, want ...string) {
	t.Helper()

	renames := client.AgentRenames()
	if len(renames) != len(want) {
		t.Fatalf("agent renames = %v, want %v", renames, want)
	}

	for i, rename := range renames {
		if rename.Name != want[i] {
			t.Errorf("agent rename %d = %q, want %q", i, rename.Name, want[i])
		}
	}
}

func TestAnAgentIsRenamedOnlyWhileItsNameDiffers(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()

	nameAgent(t, l, client, "fix-search")
	nameAgent(t, l, client, "fix-search")
	nameAgent(t, l, client, "add-tests")

	wantAgentRenames(t, client, "fix-search", "add-tests")
}

func TestAnAgentTheUserRenamedIsLeftAlone(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()
	nameAgent(t, l, client, "fix-search")

	client.SetAgentName(agentPane, "reviewer")
	nameAgent(t, l, client, "add-tests")

	wantAgentRenames(t, client, "fix-search")
}

// An agent started as `herdr agent start reviewer` already wears its name on
// the very first poll, and that name was chosen on purpose.
func TestAnAgentStartedWithANameKeepsItFromTheFirstPoll(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()
	client.SetAgentName(agentPane, "reviewer")

	nameAgent(t, l, client, "fix-search")
	nameAgent(t, l, client, "fix-search")

	wantAgentRenames(t, client)
}

// After a restart the first poll sees the names the previous run set. The namer
// remembers them, so the one wanted is the one worn, and nothing is claimed.
func TestAnAgentWearingTheNameItWasGivenLastRunIsNotClaimed(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()
	client.SetAgentName(agentPane, "fix-search")

	nameAgent(t, l, client, "fix-search")
	nameAgent(t, l, client, "add-tests")

	wantAgentRenames(t, client, "add-tests")
}

// Herdr clears an agent's name when the agent exits or is replaced. An empty
// name is nobody's, so the next agent in the pane is named.
func TestAnAgentWhoseNameHerdrClearedIsNamedAgain(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()
	nameAgent(t, l, client, "fix-search")

	client.SetAgentName(agentPane, "")
	nameAgent(t, l, client, "new-task")

	wantAgentRenames(t, client, "fix-search", "new-task")
}

func TestClearingAClaimedAgentNameHandsTheAgentBack(t *testing.T) {
	t.Parallel()

	l, client := load(""), agentSession()
	nameAgent(t, l, client, "fix-search")
	client.SetAgentName(agentPane, "reviewer")
	nameAgent(t, l, client, "fix-search")

	client.SetAgentName(agentPane, "")
	nameAgent(t, l, client, "fix-search")

	wantAgentRenames(t, client, "fix-search", "fix-search")
}

func TestAClaimedAgentNameSurvivesARestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "manual-names.json")
	client := agentSession()

	first := load(path)
	nameAgent(t, first, client, "fix-search")
	client.SetAgentName(agentPane, "reviewer")
	nameAgent(t, first, client, "fix-search")

	second := load(path)
	nameAgent(t, second, client, "fix-search")

	wantAgentRenames(t, client, "fix-search")
}
