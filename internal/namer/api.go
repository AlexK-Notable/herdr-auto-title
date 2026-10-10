package namer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultAPITimeout bounds one request, retries aside.
const DefaultAPITimeout = 30 * time.Second

// maxTokens is room for a JSON object holding a short name. Thinking is
// disabled, so none of it goes to reasoning.
const maxTokens = 256

// API asks for a name through the Anthropic Messages API.
type API struct {
	client anthropic.Client
	model  string
}

// NewAPI builds an API backend with key. Extra options, such as a base URL,
// are for tests.
func NewAPI(key, model string, opts ...option.RequestOption) *API {
	opts = append([]option.RequestOption{
		option.WithAPIKey(key),
		option.WithRequestTimeout(DefaultAPITimeout),
		option.WithMaxRetries(2),
	}, opts...)

	return &API{client: anthropic.NewClient(opts...), model: model}
}

func (a *API) String() string { return "api:" + a.model }

func (a *API) Name(ctx context.Context, req Request) (string, error) {
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(payload(req))),
		},
		// Haiku 5.5 thinks by default; disabling it is accepted at effort high
		// or below, and a name needs no reasoning.
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{Schema: schema},
		},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return "", fmt.Errorf("anthropic api: %d %s", apiErr.StatusCode, apiErr.Type())
		}

		return "", fmt.Errorf("anthropic api: %w", err)
	}

	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", errors.New("anthropic api: the model declined")
	}

	for _, block := range resp.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			var got answer
			if err := json.Unmarshal([]byte(text.Text), &got); err != nil {
				return "", fmt.Errorf("anthropic api: unreadable answer: %w", err)
			}

			if got.Name == "" {
				return "", errors.New("anthropic api: empty name")
			}

			return got.Name, nil
		}
	}

	return "", fmt.Errorf("anthropic api: no text in the answer (stop reason %s)", resp.StopReason)
}
