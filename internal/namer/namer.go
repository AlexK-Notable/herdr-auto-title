// Package namer chooses a name for each coding agent from what the user has
// been asking it to do, and for each workspace from everything going on in it,
// by asking a small model. The model is reached either
// through the `claude` CLI or the Anthropic API; everything else — when to ask,
// what to remember, how to keep names unique — is the same for both.
package namer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// DefaultModel is the model asked for a name: the smallest current one, since
// a name is a few words and is asked for often.
const DefaultModel = "claude-haiku-5-5"

// MaxName is the longest name Herdr accepts for an agent.
const MaxName = 32

// Request is what a name is chosen from. Agent, Title and Prompts describe an
// agent; Dirs, Repos and Agents a workspace.
type Request struct {
	// Task is what is being named.
	Task Task
	// Agent is the kind of agent, such as "claude".
	Agent string
	// Dir is the directory the agent works in.
	Dir string
	// Title is what the agent titled its own session, which can lag behind
	// what the user is asking for now.
	Title string
	// Prompts are the last few things the user typed, oldest first.
	Prompts []string
	// Current is the name the agent wears now, empty when it has none.
	Current string
	// Taken are the names others of its kind wear, which this one cannot have.
	Taken []string

	// Dirs are the directories open in a workspace's panes, home-relative.
	Dirs []string
	// Repos are the repositories those directories are in.
	Repos []string
	// Agents are the agents in a workspace.
	Agents []AgentBrief
}

// Backend asks a model for a name. The name it returns is raw: Clean makes it
// one Herdr accepts.
type Backend interface {
	Name(ctx context.Context, req Request) (string, error)
	// String names the backend in logs.
	String() string
}

// systemPrompt tells the model what a good agent name is. It stays fixed so a
// backend that caches prompts can reuse it.
const systemPrompt = `You name coding-agent sessions so a person juggling several of them can tell at a glance what each one is doing.

Answer with JSON {"name": "..."}. The name:
- is 2 to 4 lowercase words joined by hyphens, at most 32 characters, e.g. "fix-search-ranking" or "herdr-agent-naming";
- names the task the user is working on now, judged mostly from the latest requests;
- never names the tool, the agent or the directory alone ("claude", "config", "session");
- differs from every name listed as taken.

When a current name is given and it still fits the latest requests, answer with it unchanged: names that flicker are worse than names that are slightly stale.`

// schema constrains the answer to {"name": string}.
var schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "the name, in the form the instructions ask for",
		},
	},
	"required":             []string{"name"},
	"additionalProperties": false,
}

// schemaJSON is schema as the CLI takes it.
func schemaJSON() string {
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err) // a literal map always marshals
	}

	return string(raw)
}

// system is the instructions for what req names.
func system(req Request) string {
	if req.Task == TaskWorkspace {
		return workspacePrompt
	}

	return systemPrompt
}

// payload is the user turn: the facts about what req names, as plain text.
func payload(req Request) string {
	if req.Task == TaskWorkspace {
		return workspacePayload(req)
	}

	return agentPayload(req)
}

// Clean makes a model's answer the name req asks for, "" when none is left.
func Clean(req Request, raw string) string {
	if req.Task == TaskWorkspace {
		return Words(raw)
	}

	return Slug(raw)
}

// agentPayload is the user turn for one agent's session.
func agentPayload(req Request) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Agent: %s\n", req.Agent)

	if req.Dir != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", req.Dir)
	}

	if req.Title != "" {
		fmt.Fprintf(&b, "Session title the agent chose (may be stale): %s\n", req.Title)
	}

	if req.Current != "" {
		fmt.Fprintf(&b, "Current name (keep it if it still fits): %s\n", req.Current)
	}

	if len(req.Taken) > 0 {
		fmt.Fprintf(&b, "Taken names (do not use): %s\n", strings.Join(req.Taken, ", "))
	}

	b.WriteString("\nRecent requests from the user, oldest first:\n")

	if len(req.Prompts) == 0 {
		b.WriteString("(none yet)\n")
	}

	for i, prompt := range req.Prompts {
		fmt.Fprintf(&b, "%d. %s\n", i+1, prompt)
	}

	return b.String()
}

// answer is the structured reply both backends decode.
type answer struct {
	Name string `json:"name"`
}

var (
	notSlug    = regexp.MustCompile(`[^a-z0-9]+`)
	leadingBad = regexp.MustCompile(`^[^a-z]+`)
)

// Slug makes a model's answer a name Herdr accepts: ^[a-z][a-z0-9_-]{0,31}$.
// It cuts at a hyphen rather than mid-word when the answer is too long, and
// returns "" when nothing usable is left.
func Slug(raw string) string {
	name := notSlug.ReplaceAllString(strings.ToLower(raw), "-")
	name = leadingBad.ReplaceAllString(name, "")
	name = strings.Trim(name, "-")

	if len(name) > MaxName {
		cut := name[:MaxName]
		if i := strings.LastIndexByte(cut, '-'); i > 0 {
			cut = cut[:i]
		}

		name = strings.Trim(cut, "-")
	}

	return name
}

// Unique returns name, or name with the smallest suffix -2, -3, … that no
// taken name has. The suffix fits inside MaxName.
func Unique(name string, taken []string) string {
	if name == "" {
		return ""
	}

	used := make(map[string]struct{}, len(taken))
	for _, t := range taken {
		used[t] = struct{}{}
	}

	if _, clash := used[name]; !clash {
		return name
	}

	for n := 2; ; n++ {
		suffix := fmt.Sprintf("-%d", n)

		base := name
		if len(base)+len(suffix) > MaxName {
			base = strings.TrimRight(base[:MaxName-len(suffix)], "-")
		}

		if candidate := base + suffix; !contains(used, candidate) {
			return candidate
		}
	}
}

func contains(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
