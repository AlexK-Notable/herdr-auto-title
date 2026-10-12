// Package panel is the command line behind Herdr's panel: it lists the
// session's workspaces with who named them, and hands the running instance a
// request about one. Everything it prints is a JSON object or array.
package panel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"time"

	"github.com/kryptamine/herdr-auto-title/internal/claude"
	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/inbox"
	"github.com/kryptamine/herdr-auto-title/internal/instance"
	"github.com/kryptamine/herdr-auto-title/internal/labels"
)

// timeout bounds the one snapshot a command reads.
const timeout = 5 * time.Second

// Env is what a command needs to know about the session it runs in.
type Env struct {
	Client herdr.Client
	// Socket, StateDir and ManualPath name the session's claim and inbox, and
	// the file holding the workspaces the user named.
	Socket     string
	StateDir   string
	ManualPath string
}

type workspace struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	NamedBy string `json:"named_by"`
}

type result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// Fail prints a failure the panel shows, and returns the exit status.
func Fail(out io.Writer, message string) int {
	return emit(out, result{Message: message}, 1)
}

// Workspaces prints each workspace of the session as {id, label, named_by},
// named_by being "you" for one the user claimed and "auto-title" otherwise.
func Workspaces(ctx context.Context, out io.Writer, env Env) int {
	snapshot, ok := read(ctx, env)
	if !ok {
		return Fail(out, unreachable)
	}

	locks := labels.Load(env.ManualPath, slog.New(slog.DiscardHandler))
	list := make([]workspace, 0, len(snapshot.Workspaces))

	for _, ws := range snapshot.Workspaces {
		by := "auto-title"
		if locks.WorkspaceClaimed(ws.WorkspaceID, ws.Label) {
			by = "you"
		}

		list = append(list, workspace{ID: ws.WorkspaceID, Label: ws.Label, NamedBy: by})
	}

	return emit(out, list, 0)
}

type agent struct {
	PaneID string `json:"pane_id"`
	Name   string `json:"name"`
	// Label is what the panel shows: the name, or a stand-in while there is none.
	Label   string `json:"label"`
	Where   string `json:"where"`
	NamedBy string `json:"named_by"`
}

// Agents prints each Claude Code agent of the session, the ones Auto Title
// names, as {pane_id, name, where, named_by}: where is its workspace's label,
// and named_by "you" for a name the user gave, "auto-title" otherwise, or ""
// while it has none.
func Agents(ctx context.Context, out io.Writer, env Env) int {
	snapshot, ok := read(ctx, env)
	if !ok {
		return Fail(out, unreachable)
	}

	locks := labels.Load(env.ManualPath, slog.New(slog.DiscardHandler))
	where := workspaceOfPane(snapshot)
	list := make([]agent, 0, len(snapshot.Agents))

	for _, a := range snapshot.Agents {
		if kindOf(snapshot, a.PaneID) != claude.Agent {
			continue
		}

		by := ""

		switch {
		case a.Name == "":
		case locks.AgentClaimed(a.PaneID, a.Name):
			by = "you"
		default:
			by = "auto-title"
		}

		label := a.Name
		if label == "" {
			label = "unnamed (" + a.PaneID + ")"
		}

		list = append(list, agent{
			PaneID: a.PaneID, Name: a.Name, Label: label,
			Where: where[a.PaneID], NamedBy: by,
		})
	}

	return emit(out, list, 0)
}

// ActAgent asks the running instance to do op (an agent inbox operation) to
// the agent in pane id, and prints what became of the request.
func ActAgent(ctx context.Context, out io.Writer, env Env, op, id string) int {
	snapshot, ok := read(ctx, env)
	if !ok {
		return Fail(out, unreachable)
	}

	name, found := "", false

	for _, a := range snapshot.Agents {
		if a.PaneID == id && kindOf(snapshot, id) == claude.Agent {
			name, found = a.Name, true
		}
	}

	if !found {
		return Fail(out, "no Claude Code agent in pane "+id)
	}

	if !instance.Running(env.StateDir, env.Socket) {
		return Fail(out, "Auto Title is not running in this session")
	}

	shown := name
	if shown == "" {
		shown = "the agent in " + id
	}

	locks := labels.Load(env.ManualPath, slog.New(slog.DiscardHandler))
	if op == inbox.OpAgentRenameNow && name != "" && locks.AgentClaimed(id, name) {
		return Fail(out, shown+" was named by you; hand it back first")
	}

	err := inbox.Send(
		instance.Inbox(env.StateDir, env.Socket),
		inbox.Request{Op: op, PaneID: id},
	)
	if err != nil {
		return Fail(out, "could not reach Auto Title")
	}

	message := shown + ": asking for a name (it keeps this one if it still fits)"
	if op == inbox.OpAgentRelease {
		message = shown + ": handed back to Auto Title"
	}

	return emit(out, result{OK: true, Message: message}, 0)
}

// kindOf is the agent Herdr detected in a pane, "" for none.
func kindOf(snapshot herdr.Snapshot, paneID string) string {
	for _, p := range snapshot.Panes {
		if p.PaneID == paneID {
			return p.Agent
		}
	}

	return ""
}

// workspaceOfPane maps each pane to its workspace's label.
func workspaceOfPane(snapshot herdr.Snapshot) map[string]string {
	labelOf := make(map[string]string, len(snapshot.Workspaces))
	for _, ws := range snapshot.Workspaces {
		labelOf[ws.WorkspaceID] = ws.Label
	}

	tabWorkspace := make(map[string]string, len(snapshot.Tabs))
	for _, tab := range snapshot.Tabs {
		tabWorkspace[tab.TabID] = tab.WorkspaceID
	}

	where := make(map[string]string, len(snapshot.Panes))
	for _, p := range snapshot.Panes {
		where[p.PaneID] = labelOf[tabWorkspace[p.TabID]]
	}

	return where
}

// Act asks the running instance to do op (an inbox operation) to workspace id,
// and prints what became of the request.
func Act(ctx context.Context, out io.Writer, env Env, op, id string) int {
	snapshot, ok := read(ctx, env)
	if !ok {
		return Fail(out, unreachable)
	}

	label, found := "", false

	for _, ws := range snapshot.Workspaces {
		if ws.WorkspaceID == id {
			label, found = ws.Label, true
		}
	}

	if !found {
		return Fail(out, "no workspace "+id)
	}

	if !instance.Running(env.StateDir, env.Socket) {
		return Fail(out, "Auto Title is not running in this session")
	}

	locks := labels.Load(env.ManualPath, slog.New(slog.DiscardHandler))
	if op == inbox.OpRenameNow && locks.WorkspaceClaimed(id, label) {
		return Fail(out, label+" was named by you; hand it back first")
	}

	err := inbox.Send(
		instance.Inbox(env.StateDir, env.Socket),
		inbox.Request{Op: op, WorkspaceID: id},
	)
	if err != nil {
		return Fail(out, "could not reach Auto Title")
	}

	message := label + ": renaming soon"
	if op == inbox.OpRelease {
		message = label + ": handed back to Auto Title"
	}

	return emit(out, result{OK: true, Message: message}, 0)
}

const unreachable = "could not read the Herdr session"

func read(ctx context.Context, env Env) (herdr.Snapshot, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	snapshot, err := herdr.SessionSnapshot(ctx, env.Client)

	return snapshot, err == nil
}

func emit(out io.Writer, value any, status int) int {
	if err := json.NewEncoder(out).Encode(value); err != nil {
		return 1
	}

	return status
}
