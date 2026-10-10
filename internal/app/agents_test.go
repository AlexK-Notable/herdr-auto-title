package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/herdr/herdrtest"
	"github.com/kryptamine/herdr-auto-title/internal/namer"
	"github.com/kryptamine/herdr-auto-title/internal/reads/readstest"
)

// cannedNamer answers every request with the same name and records them.
type cannedNamer struct {
	mu    sync.Mutex
	name  string
	asked []namer.Request
}

func (c *cannedNamer) String() string { return "canned" }

func (c *cannedNamer) Name(_ context.Context, req namer.Request) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.asked = append(c.asked, req)

	return c.name, nil
}

func (c *cannedNamer) requests() []namer.Request {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]namer.Request(nil), c.asked...)
}

func typed(text string) string {
	return `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"` + text + `"}}`
}

// startNaming builds a harness whose app names agents through backend, with
// the service running until the test ends.
func startNaming(
	t *testing.T,
	client *herdrtest.Client,
	backend namer.Backend,
	homes ...string,
) *harness {
	t.Helper()

	h := startConfigured(t, client, transcriptConfig(homes...))
	service := namer.New(namer.Options{Backend: backend})
	h.app.WithAgentNamer(service)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go service.Run(ctx)

	return h
}

// pollUntil polls until done reports true, or fails after two seconds.
func pollUntil(t *testing.T, h *harness, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("gave up waiting")
		}

		h.poll()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAnAgentIsNamedFromWhatTheUserTyped(t *testing.T) {
	t.Parallel()

	home := transcript(t, typed("fix the search ranking"), typed("and add tests for it"))
	backend := &cannedNamer{name: "Fix Search Ranking"}
	h := startNaming(t, herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", Label: "1"}},
		[]herdr.PaneInfo{agentPane()},
	), backend, home)

	pollUntil(t, h, func() bool { return len(h.client.AgentRenames()) > 0 })

	if got := h.client.AgentRenames()[0]; got.PaneID != "wE:p1" ||
		got.Name != "fix-search-ranking" {
		t.Errorf("rename = %+v, want wE:p1 named fix-search-ranking", got)
	}

	asked := backend.requests()[0]
	if len(asked.Prompts) != 2 || asked.Prompts[1] != "and add tests for it" {
		t.Errorf("the model was asked with prompts %q", asked.Prompts)
	}
}

func TestAnAgentWithoutAClaudeSessionIsLeftAlone(t *testing.T) {
	t.Parallel()

	pane := agentPane()
	pane.AgentSession = nil

	backend := &cannedNamer{name: "anything"}
	h := startNaming(t, herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", Label: "1"}},
		[]herdr.PaneInfo{pane},
	), backend, stateDir(t))

	h.polls(5)
	time.Sleep(20 * time.Millisecond)
	h.polls(2)

	if len(backend.requests()) != 0 || len(h.client.AgentRenames()) != 0 {
		t.Errorf("asked %d times, renamed %v", len(backend.requests()), h.client.AgentRenames())
	}
}

func TestTwoAgentsGivenTheSameNameGetDistinctOnes(t *testing.T) {
	t.Parallel()

	home := transcript(t, typed("fix search"))
	readstest.Transcript(t, home, otherSession, typed("fix search too"))

	h := startNaming(t, herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", Label: "1"}},
		[]herdr.PaneInfo{
			agentPaneInfo("wE:p1", testSession, dashboard, true),
			agentPaneInfo("wE:p2", otherSession, dashboard, false),
		},
	), &cannedNamer{name: "fix-search"}, home)

	pollUntil(t, h, func() bool { return len(h.client.AgentRenames()) >= 2 })

	got := map[string]string{}
	for _, r := range h.client.AgentRenames() {
		got[r.PaneID] = r.Name
	}

	if got["wE:p1"] == got["wE:p2"] || got["wE:p1"] == "" || got["wE:p2"] == "" {
		t.Errorf("names = %v, want two distinct names", got)
	}
}

func TestAnAgentTheUserNamedIsNeverAskedAbout(t *testing.T) {
	t.Parallel()

	home := transcript(t, typed("review the diff"))
	client := herdrtest.New(
		[]herdr.TabInfo{{TabID: "wE:t1", Label: "1"}},
		[]herdr.PaneInfo{agentPane()},
	)
	client.SetAgentName("wE:p1", "reviewer")

	backend := &cannedNamer{name: "review-diff"}
	h := startNaming(t, client, backend, home)

	h.polls(3)
	time.Sleep(20 * time.Millisecond)
	h.polls(2)

	if len(h.client.AgentRenames()) != 0 {
		t.Errorf("renamed %v, want reviewer kept", h.client.AgentRenames())
	}

	if len(backend.requests()) != 0 {
		t.Errorf("the model was asked about an agent the user named")
	}
}
