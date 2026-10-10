package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kryptamine/herdr-auto-title/internal/claude"
	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/labels"
	"github.com/kryptamine/herdr-auto-title/internal/namer"
	"github.com/kryptamine/herdr-auto-title/internal/reads"
	"github.com/kryptamine/herdr-auto-title/internal/resolver"
	"github.com/kryptamine/herdr-auto-title/internal/state"
)

// WithAgentNamer has the app name each Claude Code agent with the names the
// service chooses. Without it, agents are left alone.
func (a *App) WithAgentNamer(service *namer.Service) *App {
	a.agents = service

	return a
}

// NamerBackend builds the backend the configuration asks for. The API key is
// read here and kept only inside the client, never in the configuration.
func NamerBackend(ctx context.Context, cfg Config) (namer.Backend, error) {
	switch cfg.Namer {
	case NamerAPI:
		key := strings.TrimSpace(os.Getenv(EnvAnthropicKey))
		if key == "" {
			var err error
			if key, err = namer.KeyFromCommand(ctx, cfg.APIKeyCommand); err != nil {
				return nil, fmt.Errorf(
					"no API key: set %s or %s: %w",
					EnvAnthropicKey,
					EnvAPIKeyCommand,
					err,
				)
			}
		}

		return namer.NewAPI(key, cfg.NamerModel, cfg.NamerWorkspace), nil
	case NamerCLI:
		return namer.CLI{Binary: cfg.ClaudeBin, Model: cfg.NamerModel}, nil
	default:
		return nil, errors.New("unknown namer " + cfg.Namer)
	}
}

// nameAgents gives each Claude Code agent the name chosen for its session. The
// names come from the service's memory, so a poll never waits on the model;
// a session whose name is due is queued and named by a later poll.
func (a *App) nameAgents(
	ctx context.Context,
	pass *labels.Pass,
	poll *reads.Poll,
	snapshot herdr.Snapshot,
	tabs []state.TabState,
) {
	panes := make(map[string]*state.PaneState)

	for _, tab := range tabs {
		for _, pane := range tab.Panes {
			panes[pane.ID] = pane
		}
	}

	worn := make(map[string]string, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		worn[agent.PaneID] = agent.Name
	}

	for _, agent := range snapshot.Agents {
		if ctx.Err() != nil {
			return
		}

		pane := panes[agent.PaneID]
		if pane == nil || pass.AgentLocked(agent.PaneID) {
			continue
		}

		a.nameAgent(ctx, pass, poll, agent, pane, takenBy(worn, agent.PaneID))
	}
}

// nameAgent gives one agent its remembered name and queues a new one when due.
func (a *App) nameAgent(
	ctx context.Context,
	pass *labels.Pass,
	poll *reads.Poll,
	agent herdr.AgentInfo,
	pane *state.PaneState,
	taken []string,
) {
	key, fromTranscript, ok := agentKey(agent, pane)
	if !ok {
		return
	}

	// Judged before the model is asked: a name the user gave is claimed here,
	// on the first poll that sees it, and never costs a request.
	pass.Agent(ctx, agent.PaneID, agent.Name, resolver.Decision{
		Name:   namer.Unique(a.agents.Remembered(key), taken),
		Reason: "named by " + a.agents.Backend(),
	})

	if pass.AgentLocked(agent.PaneID) {
		return
	}

	poll.Fill(ctx, pane)

	title := pane.AgentAITitle
	if !fromTranscript {
		// Herdr's integration never reported this session, so there is no
		// transcript to read: name it once from the title Claude Code shows in
		// its terminal, which it derived from the user's own prompts.
		title = pane.TerminalTitle
	}

	// Queues a new name when one is due; a later poll applies it. The prompt
	// count says when there is something new to name it from, and nothing
	// typed and no title says there is nothing yet.
	version := strconv.Itoa(pane.AgentTurns)
	if pane.AgentTurns == 0 && title == "" {
		version = ""
	}

	a.agents.Name(key, version, namer.Request{
		Agent:   pane.Agent,
		Dir:     agentDir(pane),
		Title:   title,
		Prompts: pane.AgentPrompts,
		Current: agent.Name,
		Taken:   taken,
	})
}

// agentKey is what an agent's name is remembered by: its Claude session, or,
// when Herdr's integration never reported one, its pane — which a new agent in
// the same pane inherits until its own session is reported. fromTranscript
// says whether a transcript can be read; ok is false for an agent not named.
func agentKey(agent herdr.AgentInfo, pane *state.PaneState) (key string, fromTranscript, ok bool) {
	if session, found := pane.AgentSession.IDFor(claude.Agent); found {
		return session, true, true
	}

	if pane.Agent != claude.Agent || pane.TerminalTitle == "" {
		return "", false, false
	}

	return "pane:" + agent.PaneID, false, true
}

// takenBy is every name other agents wear, which this one cannot have.
func takenBy(worn map[string]string, self string) []string {
	taken := make([]string, 0, len(worn))

	for pane, name := range worn {
		if pane != self && name != "" {
			taken = append(taken, name)
		}
	}

	return taken
}

// agentDir is where the agent works, shortened to the home-relative path a
// person would recognise.
func agentDir(pane *state.PaneState) string {
	dir := pane.AgentDir
	if dir == "" {
		dir = pane.Dir
	}

	return homeRelative(dir)
}

// homeRelative shortens a path under the home directory to the ~/ form a
// person would recognise.
func homeRelative(dir string) string {
	if dir == "" {
		return ""
	}

	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}

	return dir
}
