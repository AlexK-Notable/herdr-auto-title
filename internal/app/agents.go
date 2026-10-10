package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

		session, ok := pane.AgentSession.IDFor(claude.Agent)
		if !ok {
			continue
		}

		// Judged before the model is asked: a name the user gave is claimed
		// here, on the first poll that sees it, and never costs a request.
		taken := takenBy(worn, agent.PaneID)
		pass.Agent(ctx, agent.PaneID, agent.Name, resolver.Decision{
			Name:   namer.Unique(a.agents.Remembered(session), taken),
			Reason: "named by " + a.agents.Backend(),
		})

		if pass.AgentLocked(agent.PaneID) {
			continue
		}

		poll.Fill(ctx, pane)

		// Queues a new name when one is due; a later poll applies it.
		a.agents.Name(session, pane.AgentTurns, namer.Request{
			Agent:   pane.Agent,
			Dir:     agentDir(pane),
			Title:   pane.AgentAITitle,
			Prompts: pane.AgentPrompts,
			Current: agent.Name,
			Taken:   taken,
		})
	}
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

	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}

	return dir
}
