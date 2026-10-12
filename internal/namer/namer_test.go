package namer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

var herdrName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestSlugMakesAnyAnswerANameHerdrAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"fix-search-ranking":      "fix-search-ranking",
		"Fix Search Ranking":      "fix-search-ranking",
		"  herdr: agent naming! ": "herdr-agent-naming",
		"42-answers":              "answers",
		"--":                      "",
		"ünïcode wörds":           "n-code-w-rds",
		"a-very-long-name-that-keeps-going-past-the-limit": "a-very-long-name-that-keeps",
	}

	for raw, want := range cases {
		got := Slug(raw)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", raw, got, want)
		}

		if got != "" && !herdrName.MatchString(got) {
			t.Errorf("Slug(%q) = %q, which Herdr refuses", raw, got)
		}
	}
}

func TestUniqueSuffixesOnlyAClashingName(t *testing.T) {
	t.Parallel()

	if got := Unique("fix-search", []string{"other"}); got != "fix-search" {
		t.Errorf("no clash: got %q", got)
	}

	if got := Unique("fix-search", []string{"fix-search", "fix-search-2"}); got != "fix-search-3" {
		t.Errorf("clash: got %q, want fix-search-3", got)
	}

	long := strings.Repeat("a", MaxName)
	if got := Unique(long, []string{long}); len(got) > MaxName || !herdrName.MatchString(got) {
		t.Errorf("long clash: got %q", got)
	}
}

func TestThePayloadCarriesTheFactsAndNoInstructionsFromThem(t *testing.T) {
	t.Parallel()

	got := payload(Request{
		Agent: "claude", Dir: "/home/u/repo", Title: "old title",
		Prompts: []string{"first", "second"}, Current: "fix-search", Taken: []string{"a", "b"},
	})

	for _, want := range []string{"Agent: claude", "/home/u/repo", "old title", "Current name", "fix-search", "Taken names (do not use): a, b", "1. first", "2. second"} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %q:\n%s", want, got)
		}
	}
}

func TestTheAPIBackendAsksHaikuForStructuredOutputWithoutThinking(t *testing.T) {
	t.Parallel()

	var (
		body      map[string]any
		workspace string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)

		workspace = r.Header.Get("Anthropic-Workspace-Id")

		if r.Header.Get("X-Api-Key") != "sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5",
			"content":[{"type":"text","text":"{\"name\":\"herdr-agent-naming\"}"}],
			"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`,
		)
	}))
	defer server.Close()

	api := NewAPI(
		"sk-test", DefaultModel, "wrkspc_test",
		option.WithBaseURL(server.URL), option.WithMaxRetries(0),
	)

	name, err := api.Name(context.Background(), Request{Agent: "claude", Prompts: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}

	if name != "herdr-agent-naming" {
		t.Errorf("name = %q", name)
	}

	if workspace != "wrkspc_test" {
		t.Errorf("workspace header = %q, want wrkspc_test", workspace)
	}

	if body["model"] != DefaultModel {
		t.Errorf("model = %v", body["model"])
	}

	if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != "disabled" {
		t.Errorf("thinking = %v, want disabled", body["thinking"])
	}

	cfg, _ := body["output_config"].(map[string]any)
	if cfg["effort"] != "low" {
		t.Errorf("effort = %v, want low", cfg["effort"])
	}

	if format, _ := cfg["format"].(map[string]any); format["type"] != "json_schema" {
		t.Errorf("format = %v, want json_schema", cfg["format"])
	}

	for _, banned := range []string{"temperature", "top_p", "top_k"} {
		if _, sent := body[banned]; sent {
			t.Errorf("%s was sent; Haiku 5.5 refuses non-default sampling", banned)
		}
	}
}

func TestTheAPIBackendReportsAStatusWithoutTheKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(
			w,
			`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		)
	}))
	defer server.Close()

	_, err := NewAPI(
		"sk-secret-value",
		DefaultModel,
		"",
		option.WithBaseURL(server.URL),
		option.WithMaxRetries(0),
	).
		Name(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want a 401", err)
	}

	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Error("the key leaked into the error")
	}
}

func TestUniqueCutsTheBaseAtAWord(t *testing.T) {
	t.Parallel()

	name := "herdr-plugin-auto-naming-survey"
	if got := Unique(name, []string{name}); got != "herdr-plugin-auto-naming-2" {
		t.Errorf("Unique = %q, want herdr-plugin-auto-naming-2", got)
	}
}

func TestSlugKeepsAWordThatEndsAtTheLimit(t *testing.T) {
	t.Parallel()

	// The fourth word ends at exactly 32 characters.
	in := "aaaaaaa-bbbbbbb-ccccccc-dddddddd-eeee"
	if got := Slug(in); got != "aaaaaaa-bbbbbbb-ccccccc-dddddddd" {
		t.Errorf("Slug = %q, want aaaaaaa-bbbbbbb-ccccccc-dddddddd", got)
	}
}
