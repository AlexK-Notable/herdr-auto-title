// Package herdr implements a client for the Herdr local socket API: NDJSON over
// the socket named by HERDR_SOCKET_PATH — a named pipe on Windows — with one
// request per connection. Verified against Herdr v0.8.2, protocol 20.
package herdr

import (
	"encoding/json"
	"errors"
	"fmt"
)

// request is one outbound line. Herdr requires "params" on every method, so
// methods without parameters take an empty object rather than omitting it.
type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// frame is one inbound line: a result or an error.
type frame struct {
	Result json.RawMessage `json:"result"`
	Error  *APIError       `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("herdr api error %s: %s", e.Code, e.Message)
}

// Error codes Auto Title reacts to.
const (
	// CodeTabNotFound is returned when a tab closed between the snapshot that
	// named it and the rename that followed.
	CodeTabNotFound = "tab_not_found"
	// CodeWorkspaceNotFound is the same for a workspace, which can be closed
	// between the snapshot and the rename it decided on.
	CodeWorkspaceNotFound = "workspace_not_found"
	// CodePaneNotFound is the same for a pane, which can close between the
	// snapshot that listed it and the read of what is running in it.
	CodePaneNotFound = "pane_not_found"
	// CodeAgentNotFound is answered when the pane holds no agent any more, or
	// is gone: probed live against Herdr 0.9.2.
	CodeAgentNotFound = "agent_not_found"
	// CodeAgentNameTaken is answered when another live agent already carries
	// the name: agent names are unique across the session.
	CodeAgentNameTaken = "agent_name_taken"
	// CodeInvalidAgentName is answered for a name outside
	// ^[a-z][a-z0-9_-]{0,31}$, which nothing here should ever send.
	CodeInvalidAgentName = "invalid_agent_name"
	// CodeUIBusy is answered when a popup is already open: Herdr shows one
	// at a time.
	CodeUIBusy = "ui_busy"
)

// ErrUnanswered marks a call that failed after its request was sent. Herdr
// carries out a request it has read even once the caller hangs up, so such a
// call may still take effect.
var ErrUnanswered = errors.New("no answer from herdr")

// ErrorCode returns the Herdr error code carried by err, or "" if err is not a
// Herdr API error.
func ErrorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}

	return ""
}

// Method names used by Auto Title: two to read the session, the ones that act
// on it, one to tell the user how a restart went, and one to open the
// settings panel.
const (
	MethodSessionSnapshot         = "session.snapshot"
	MethodPaneProcessInfo         = "pane.process_info"
	MethodTabRename               = "tab.rename"
	MethodPaneRename              = "pane.rename"
	MethodAgentRename             = "agent.rename"
	MethodWorkspaceRename         = "workspace.rename"
	MethodWorkspaceReportMetadata = "workspace.report_metadata"
	MethodNotificationShow        = "notification.show"
	MethodPluginPaneOpen          = "plugin.pane.open"
)

// PluginPaneOpenParams opens one of a plugin's [[panes]] entries. A popup
// takes its placement and size from the manifest.
type PluginPaneOpenParams struct {
	PluginID   string `json:"plugin_id"`
	Entrypoint string `json:"entrypoint"`
	Focus      bool   `json:"focus"`
}

type PaneTarget struct {
	PaneID string `json:"pane_id"`
}

type TabRenameParams struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

type PaneRenameParams struct {
	PaneID string `json:"pane_id"`
	Label  string `json:"label"`
}

// WorkspaceRenameParams labels a workspace. Herdr keeps the label from then on
// and no longer derives it from the workspace's directory.
type WorkspaceRenameParams struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// AgentRenameParams names the agent in a pane. Target takes a pane id or a
// live agent name; a nil Name clears it, which Auto Title never sends.
type AgentRenameParams struct {
	Target string  `json:"target"`
	Name   *string `json:"name"`
}

// NotificationParams is a notice for the user. Only the title is required;
// Herdr also takes a position and a sound, which nothing here sets.
type NotificationParams struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// NotificationResult is what notification.show answers: whether the notice
// was shown, and one of Herdr's reasons when it was not.
type NotificationResult struct {
	Shown  bool   `json:"shown"`
	Reason string `json:"reason"`
}

// TopicToken is the one workspace token Auto Title reports, which a sidebar row
// draws where the user's layout says `$topic`.
const TopicToken = "topic"

// WorkspaceMetadataParams reports display tokens on a workspace. A nil value
// clears its token, which is why the map holds pointers: leaving a key out
// changes nothing.
type WorkspaceMetadataParams struct {
	WorkspaceID string             `json:"workspace_id"`
	Source      string             `json:"source"`
	Tokens      map[string]*string `json:"tokens"`
	TTLMs       int64              `json:"ttl_ms"`
}

type emptyParams struct{}
