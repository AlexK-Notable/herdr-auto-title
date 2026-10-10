package namer

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxWorkspaceWords and MaxWorkspaceName bound a workspace's name: it shares
// the sidebar with the workspace's number and its agents' rows.
const (
	MaxWorkspaceWords = 3
	MaxWorkspaceName  = 40
)

// Task is what is being named. Each has its own instructions and its own shape
// of name; everything else — when to ask, what to remember — is shared.
type Task int

const (
	// TaskAgent names one coding agent after what the user is asking it to do.
	TaskAgent Task = iota
	// TaskWorkspace names a workspace after everything going on in it.
	TaskWorkspace
)

func (t Task) String() string {
	if t == TaskWorkspace {
		return "workspace"
	}

	return "agent"
}

// AgentBrief is one agent in a workspace, as the workspace is named from it.
type AgentBrief struct {
	Name string
	// Request is the last thing the user asked it, or what its terminal title
	// says it is doing when nothing was read.
	Request string
}

// workspacePrompt tells the model what a good workspace name is.
const workspacePrompt = `You name terminal workspaces so a person juggling several of them can tell at a glance what each one is for.

A workspace holds terminals and coding agents working on one effort. Answer with JSON {"name": "..."}. The name:
- is 1 to 3 lowercase words separated by spaces, at most 40 characters, e.g. "herdr naming", "desktop config" or "znote search";
- names the effort the workspace is for, judged from its directories, repositories, agents and the user's latest requests together;
- prefers the project or theme the agents share over any one agent's task;
- never is a bare directory name unless nothing else is known;
- differs from every name listed as taken.

When a current name is given and it still fits, answer with it unchanged: names that flicker are worse than names that are slightly stale.`

// workspacePayload is the user turn for a workspace: its facts, as plain text.
func workspacePayload(req Request) string {
	var b strings.Builder

	list := func(label string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(items, ", "))
		}
	}

	list("Directories open in its terminals", req.Dirs)
	list("Repositories", req.Repos)

	if req.Current != "" {
		fmt.Fprintf(&b, "Current name (keep it if it still fits): %s\n", req.Current)
	}

	list("Taken names (do not use)", req.Taken)

	b.WriteString("\nAgents in it, with the user's latest request to each:\n")

	if len(req.Agents) == 0 {
		b.WriteString("(none)\n")
	}

	for _, agent := range req.Agents {
		name := agent.Name
		if name == "" {
			name = "(unnamed)"
		}

		request := agent.Request
		if request == "" {
			request = "(nothing yet)"
		}

		fmt.Fprintf(&b, "- %s: %s\n", name, request)
	}

	return b.String()
}

var word = regexp.MustCompile(`[a-z0-9]+`)

// Words makes a model's answer a workspace name: at most MaxWorkspaceWords
// lowercase words, separated by single spaces, cut at a word rather than
// mid-word to fit MaxWorkspaceName. It returns "" when no word is left.
func Words(raw string) string {
	words := word.FindAllString(strings.ToLower(raw), -1)
	if len(words) > MaxWorkspaceWords {
		words = words[:MaxWorkspaceWords]
	}

	for len(words) > 1 && len(strings.Join(words, " ")) > MaxWorkspaceName {
		words = words[:len(words)-1]
	}

	name := strings.Join(words, " ")
	if len(name) > MaxWorkspaceName {
		name = name[:MaxWorkspaceName]
	}

	return name
}
