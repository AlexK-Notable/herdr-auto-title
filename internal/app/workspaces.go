package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kryptamine/herdr-auto-title/internal/git"
	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/inbox"
	"github.com/kryptamine/herdr-auto-title/internal/labels"
	"github.com/kryptamine/herdr-auto-title/internal/namer"
	"github.com/kryptamine/herdr-auto-title/internal/reads"
	"github.com/kryptamine/herdr-auto-title/internal/resolver"
	"github.com/kryptamine/herdr-auto-title/internal/state"
)

// workspaceKeyPrefix keeps a workspace's name apart from the agent sessions
// the same service remembers.
const workspaceKeyPrefix = "ws:"

// WithWorkspaceNamer has the app label each workspace with the names the
// service chooses. Without it, workspace labels are left to Herdr.
func (a *App) WithWorkspaceNamer(service *namer.Service) *App {
	a.spaces = service

	return a
}

// WithInbox has the app act on the requests left in dir by the command line.
func (a *App) WithInbox(dir string) *App {
	a.inbox = dir

	return a
}

// serveInbox applies the requests waiting for this session. A locked workspace
// or agent is the user's: only a release touches it.
func (a *App) serveInbox(pass *labels.Pass) {
	if a.inbox == "" {
		return
	}

	for _, req := range inbox.Drain(a.inbox) {
		switch req.Op {
		case inbox.OpAgentRelease, inbox.OpAgentRenameNow:
			a.serveAgentRequest(pass, req)
		default:
			a.serveWorkspaceRequest(pass, req)
		}
	}
}

func (a *App) serveWorkspaceRequest(pass *labels.Pass, req inbox.Request) {
	key := workspaceKeyPrefix + req.WorkspaceID

	switch req.Op {
	case inbox.OpRelease:
		if a.labels.ReleaseWorkspace(req.WorkspaceID) {
			a.log.Info("a workspace was handed back", "workspace_id", req.WorkspaceID)
		}

		a.forceName(key)
	case inbox.OpRenameNow:
		if !pass.WorkspaceLocked(req.WorkspaceID) {
			a.forceName(key)
		}
	}
}

func (a *App) serveAgentRequest(pass *labels.Pass, req inbox.Request) {
	switch req.Op {
	case inbox.OpAgentRelease:
		if a.labels.ReleaseAgent(req.PaneID) {
			a.log.Info("an agent was handed back", "pane_id", req.PaneID)
		}

		a.forceAgent(req.PaneID)
	case inbox.OpAgentRenameNow:
		if !pass.AgentLocked(req.PaneID) {
			a.forceAgent(req.PaneID)
		}
	}
}

// forceAgent has the agent in a pane asked about on the next poll that names
// it, whatever the interval says. Its name is kept by session, which only
// that poll works out, so the pane waits here until then.
func (a *App) forceAgent(paneID string) {
	if a.agents == nil {
		return
	}

	if a.agentsDue == nil {
		a.agentsDue = make(map[string]struct{})
	}

	a.agentsDue[paneID] = struct{}{}
}

func (a *App) forceName(key string) {
	if a.spaces != nil {
		a.spaces.Force(key)
	}
}

// workspace is what one poll gathered about a workspace.
type workspace struct {
	info  herdr.WorkspaceInfo
	panes []*state.PaneState
	// folders are the names of the directories its panes were in when the
	// snapshot was taken, which is what Herdr labels an unnamed one after.
	folders []string
}

// nameWorkspaces gives each workspace the name chosen for it, after the agents
// were named so that their names are among what it is named from. As with
// agents, a poll never waits on the model.
func (a *App) nameWorkspaces(
	ctx context.Context,
	pass *labels.Pass,
	poll *reads.Poll,
	snapshot herdr.Snapshot,
	tabs []state.TabState,
) {
	worn := make(map[string]string, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		worn[agent.PaneID] = agent.Name
	}

	gathered := workspacesIn(snapshot, tabs)

	for _, ws := range gathered {
		if ctx.Err() != nil {
			return
		}

		if pass.WorkspaceLocked(ws.info.WorkspaceID) {
			continue
		}

		a.nameWorkspace(ctx, pass, poll, ws, worn, labelsBesides(gathered, ws))
	}
}

// nameWorkspace gives one workspace its remembered name and queues a new one
// when its contents changed.
func (a *App) nameWorkspace(
	ctx context.Context,
	pass *labels.Pass,
	poll *reads.Poll,
	ws workspace,
	worn map[string]string,
	taken []string,
) {
	for _, pane := range ws.panes {
		poll.Fill(ctx, pane)
	}

	req := workspaceRequest(ws, worn)
	req.Current, req.Taken = ws.info.Label, taken

	key := workspaceKeyPrefix + ws.info.WorkspaceID

	// Judged before the model is asked, so a label the user typed is claimed
	// on sight and never costs a request.
	pass.Workspace(ctx, ws.info.WorkspaceID, ws.info.Label,
		append(slices.Clone(ws.folders), req.Repos...),
		resolver.Decision{
			Name:   a.spaces.Remembered(key),
			Reason: "named by " + a.spaces.Backend(),
		})

	if pass.WorkspaceLocked(ws.info.WorkspaceID) {
		return
	}

	a.spaces.Name(key, fingerprint(req), req)
}

// workspacesIn groups the poll's panes by workspace, in Herdr's order.
func workspacesIn(snapshot herdr.Snapshot, tabs []state.TabState) []workspace {
	workspaceOf := make(map[string]string, len(snapshot.Tabs))
	for _, tab := range snapshot.Tabs {
		workspaceOf[tab.TabID] = tab.WorkspaceID
	}

	index := make(map[string]int, len(snapshot.Workspaces))
	gathered := make([]workspace, len(snapshot.Workspaces))

	for i, info := range snapshot.Workspaces {
		index[info.WorkspaceID] = i
		gathered[i].info = info
	}

	for _, tab := range tabs {
		if i, ok := index[workspaceOf[tab.ID]]; ok {
			gathered[i].panes = append(gathered[i].panes, tab.Panes...)
		}
	}

	for _, pane := range snapshot.Panes {
		if i, ok := index[workspaceOf[pane.TabID]]; ok {
			gathered[i].folders = appendNew(gathered[i].folders,
				baseName(pane.CWD), baseName(pane.ForegroundCWD))
		}
	}

	return gathered
}

// workspaceRequest is what a workspace is named from: where its panes are, the
// repositories those are in, and what each of its agents was last asked.
func workspaceRequest(ws workspace, worn map[string]string) namer.Request {
	req := namer.Request{Task: namer.TaskWorkspace}

	for _, pane := range ws.panes {
		req.Dirs = appendNew(req.Dirs, homeRelative(pane.Dir))
		req.Repos = appendNew(req.Repos, repoName(pane.Git), repoName(pane.AgentGit))

		if pane.Agent != "" {
			req.Agents = append(req.Agents, namer.AgentBrief{
				Name:    worn[pane.ID],
				Request: lastRequest(pane),
			})
		}
	}

	slices.Sort(req.Dirs)
	slices.Sort(req.Repos)
	slices.SortFunc(req.Agents, func(x, y namer.AgentBrief) int {
		return strings.Compare(x.Name+"\x00"+x.Request, y.Name+"\x00"+y.Request)
	})

	return req
}

// fingerprint stands for what a workspace is named from, so a change in any of
// it is noticed and nothing else is: not its own label, not the others'. It is
// empty when there is nothing to name it from.
func fingerprint(req namer.Request) string {
	if len(req.Dirs) == 0 && len(req.Agents) == 0 {
		return ""
	}

	h := sha256.New()

	for _, part := range [][]string{req.Dirs, req.Repos} {
		h.Write([]byte(strings.Join(part, "\x00") + "\x01"))
	}

	for _, agent := range req.Agents {
		h.Write([]byte(agent.Name + "\x00" + agent.Request + "\x01"))
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// lastRequest is the latest thing the user asked an agent, or, when nothing
// was read, what the agent's session or terminal says it is doing.
func lastRequest(pane *state.PaneState) string {
	if n := len(pane.AgentPrompts); n > 0 {
		return pane.AgentPrompts[n-1]
	}

	if pane.AgentAITitle != "" {
		return pane.AgentAITitle
	}

	return pane.TerminalTitle
}

// repoName is the name of the repository a checkout belongs to — the main one
// for a worktree — and "" outside any.
func repoName(checkout git.Checkout) string {
	if checkout.CommonDir == "" {
		return ""
	}

	dir := filepath.Clean(checkout.CommonDir)
	if filepath.Base(dir) == ".git" {
		return filepath.Base(filepath.Dir(dir))
	}

	return strings.TrimSuffix(filepath.Base(dir), ".git")
}

// baseName is the last element of a path, "" for none.
func baseName(path string) string {
	if path == "" {
		return ""
	}

	return filepath.Base(path)
}

// labelsBesides is every other workspace's label, once each.
func labelsBesides(all []workspace, self workspace) []string {
	var taken []string

	for _, ws := range all {
		if ws.info.WorkspaceID != self.info.WorkspaceID {
			taken = appendNew(taken, ws.info.Label)
		}
	}

	slices.Sort(taken)

	return taken
}

// appendNew appends each non-empty item not already in list.
func appendNew(list []string, items ...string) []string {
	for _, item := range items {
		if item != "" && !slices.Contains(list, item) {
			list = append(list, item)
		}
	}

	return list
}
