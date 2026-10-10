// Package labels keeps the labels Herdr shows in step with the names chosen
// for them: it tells a label the user wrote from its own, leaves the user's
// alone, sends the renames and remembers what became of each.
package labels

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"strconv"
	"sync"

	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/resolver"
	"github.com/kryptamine/herdr-auto-title/internal/state"
)

// Labels outlives a poll because what it remembers does: the label each thing
// last carried, and the ones the user claimed, which are kept on disk too.
type Labels struct {
	tabs   *claims
	panes  *claims
	agents *claims
	log    *slog.Logger

	mu   sync.Mutex
	path string
	// settled is false until the first poll has finished, while nothing can yet
	// be judged. One poll looks at every kind together, so they share it.
	settled bool
}

// Load reads the claims an earlier run left at path. Anything unreadable
// yields none: keeping them is a convenience, not a reason to refuse to start.
// An empty path keeps them in memory only.
func Load(path string, log *slog.Logger) *Labels {
	l := &Labels{path: path, log: log}
	l.tabs = newClaims(l, tabKind)
	l.panes = newClaims(l, paneKind)
	l.agents = newClaims(l, agentKind)

	raw, err := os.ReadFile(path) //nolint:gosec // the path is configured, never terminal-derived
	if err != nil {
		return l
	}

	var stored claimsFile
	if json.Unmarshal(raw, &stored) != nil {
		return l
	}

	maps.Copy(l.tabs.locked, stored.Locked)
	maps.Copy(l.panes.locked, stored.LockedPanes)
	maps.Copy(l.agents.locked, stored.LockedAgents)

	return l
}

// Pass opens one poll's pass over the snapshot's labels. It forgets what the
// session no longer holds and releases a claim whose label has moved on, so
// that nothing asked of the pass is answered from a session that is gone.
func (l *Labels) Pass(client herdr.Client, snapshot herdr.Snapshot) *Pass {
	l.tabs.retain(labelsOf(snapshot.Tabs, func(tab herdr.TabInfo) (string, string) {
		return tab.TabID, tab.Label
	}))
	// A pane Herdr has never been asked to name carries no label at all, which
	// arrives here as the empty string.
	l.panes.retain(labelsOf(snapshot.Panes, func(pane herdr.PaneInfo) (string, string) {
		return pane.PaneID, pane.Label
	}))

	// An agent with no name is left out of the list or carries null, both of
	// which arrive here as the empty string.
	l.agents.retain(labelsOf(snapshot.Agents, func(agent herdr.AgentInfo) (string, string) {
		return agent.PaneID, agent.Name
	}))

	return &Pass{labels: l, client: client}
}

func labelsOf[T any](things []T, of func(T) (id, label string)) map[string]string {
	labels := make(map[string]string, len(things))

	for _, thing := range things {
		id, label := of(thing)
		labels[id] = label
	}

	return labels
}

// Pass is one poll's pass over the labels. It is a thing of its own so that
// nothing is judged before the claims were pruned to the session being read.
type Pass struct {
	labels *Labels
	client herdr.Client
}

func (p *Pass) TabLocked(id string) bool {
	return p.labels.tabs.isLocked(id)
}

func (p *Pass) PaneLocked(id string) bool {
	return p.labels.panes.isLocked(id)
}

func (p *Pass) AgentLocked(paneID string) bool {
	return p.labels.agents.isLocked(paneID)
}

// Tab gives a tab the name chosen for it, unless the user put the label it
// carries there. A claimed tab is not looked at again until its label moves.
func (p *Pass) Tab(ctx context.Context, tab state.TabState, decision resolver.Decision) {
	claims := p.labels.tabs
	if claims.isLocked(tab.ID) {
		return
	}

	p.apply(ctx, claims, sightingOfTab(tab, decision.Name), decision)
}

// Pane gives a pane the name chosen for it, on the terms Tab gives a tab its.
func (p *Pass) Pane(ctx context.Context, pane *state.PaneState, decision resolver.Decision) {
	claims := p.labels.panes
	if claims.isLocked(pane.ID) {
		return
	}

	p.apply(ctx, claims, sightingOfPane(pane, decision.Name), decision)
}

// Agent gives the agent in a pane the name chosen for it, on the terms Tab
// gives a tab its — with one difference: a name the agent already wears when
// first sighted is the user's unless it is the one chosen for it, because an
// agent started as `herdr agent start reviewer` is named on purpose.
func (p *Pass) Agent(ctx context.Context, paneID, current string, decision resolver.Decision) {
	claims := p.labels.agents
	if claims.isLocked(paneID) {
		return
	}

	p.apply(ctx, claims, sighting{id: paneID, current: current, desired: decision.Name}, decision)
}

// Settle ends a pass that saw everything the session holds. A pass cut short
// must not: what it missed would look new on the next one, and already named.
func (p *Pass) Settle() {
	p.labels.settle()
}

// apply judges the label a thing carries and renames it when nobody claims
// it. Nothing that goes wrong here is worth cutting the poll short: the next
// one decides again from state read again.
func (p *Pass) apply(
	ctx context.Context,
	claims *claims,
	seen sighting,
	decision resolver.Decision,
) {
	kind := claims.kind
	log := p.labels.log
	idKey := kind.noun + "_id"

	switch claims.observe(seen) {
	case verdictClaimed:
		log.Info("leaving a "+kind.noun+" the user renamed", idKey, seen.id, "name", seen.current)
		return
	case verdictName:
	}

	if decision.Name == "" || decision.Name == seen.current {
		return
	}

	if err := kind.rename(ctx, p.client, seen.id, decision.Name); err != nil {
		if herdr.ErrorCode(err) == kind.gone {
			log.Debug(kind.noun+" closed before it could be renamed", idKey, seen.id)
			return
		}

		if errors.Is(err, herdr.ErrUnanswered) {
			// Herdr may still apply it: docs/architecture/manual-rename-protection.md.
			claims.sent(seen.id, decision.Name)
		}

		log.Warn(kind.noun+" rename failed", idKey, seen.id, "name", decision.Name, "error", err)

		return
	}

	// Recorded before the log line so the next poll cannot read this rename as
	// the user's.
	claims.applied(seen.id, decision.Name)
	log.Info(kind.noun+" renamed",
		idKey, seen.id,
		"old", seen.current,
		"new", decision.Name,
		"reason", decision.Reason,
		"confidence", decision.Confidence,
	)
}

// kind is what differs between the things Auto Title names.
type kind struct {
	noun   string
	rename func(ctx context.Context, c herdr.Client, id, label string) error
	// gone is the error Herdr answers when the thing closed between the
	// snapshot and the rename. The next poll will not see it at all.
	gone string
	// followsMoves takes a thing sighted wearing an unclaimed departed label as
	// the same one, moved. Only a pane: Herdr moves one across workspaces under
	// a new id, label intact, while a moved tab keeps its id.
	followsMoves bool
	// claimsFirstSights judges names already present on the first poll instead
	// of passing them. A tab or pane cannot: Auto Title's own labels from a
	// previous run look the same. An agent can, because the namer remembers the
	// names it chose across runs and passes them in as the desired name.
	claimsFirstSights bool
}

var (
	tabKind = kind{
		noun:   "tab",
		rename: herdr.RenameTab,
		gone:   herdr.CodeTabNotFound,
	}
	paneKind = kind{
		noun:         "pane",
		rename:       herdr.RenamePane,
		gone:         herdr.CodePaneNotFound,
		followsMoves: true,
	}
	// An agent is addressed by the pane it occupies. Herdr clears its name when
	// the agent exits or is replaced, so unnamed is only ever the empty string.
	agentKind = kind{
		noun:              "agent",
		rename:            herdr.RenameAgent,
		gone:              herdr.CodeAgentNotFound,
		followsMoves:      true,
		claimsFirstSights: true,
	}
)

// sightingOfTab is what a poll saw of a tab, given the name chosen for it.
// What Herdr calls an unclaimed tab is its position.
func sightingOfTab(tab state.TabState, desired string) sighting {
	return sighting{
		id:        tab.ID,
		current:   tab.CurrentName,
		desired:   desired,
		byDefault: strconv.Itoa(tab.Position),
	}
}

// sightingOfPane is what a poll saw of a pane. A pane has one spelling for
// unnamed and no second default: pane.rename clears an empty label rather than
// storing it, so byDefault stays empty.
func sightingOfPane(pane *state.PaneState, desired string) sighting {
	return sighting{
		id:      pane.ID,
		current: pane.CurrentName,
		desired: desired,
	}
}
