package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"drishtimap/app"
)

// claude talks to the Claude API through the official SDK.
type claude struct {
	client   anthropic.Client
	model    string
	msgs     []anthropic.BetaMessageParam
	tools    []anthropic.BetaToolUnionParam
	fallback bool
}

// Models that accept server-side refusal fallbacks: a request the safety classifiers decline
// is re-served by another Claude model inside the same call instead of simply stopping.
var fallbackModels = map[string]bool{"claude-fable-5-2": true, "claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true}

func newClaude(p app.Provider, key string) *claude {
	opts := []option.RequestOption{}
	if key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	if p.BaseURL != "" && p.BaseURL != "https://api.anthropic.com" {
		opts = append(opts, option.WithBaseURL(p.BaseURL))
	}
	c := &claude{client: anthropic.NewClient(opts...), model: p.Model, fallback: fallbackModels[p.Model]}
	for _, t := range app.Tools {
		var s struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		json.Unmarshal(t.Params, &s)
		if s.Properties == nil {
			s.Properties = map[string]any{}
		}
		tp := anthropic.BetaToolParam{Name: t.Name, Description: anthropic.String(t.Desc),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: s.Properties, Required: s.Required}}
		c.tools = append(c.tools, anthropic.BetaToolUnionParam{OfTool: &tp})
	}
	return c
}

func (c *claude) step(ctx context.Context, userText string, results []ToolResult) (*Reply, error) {
	if len(results) > 0 {
		// all tool results go back in ONE user message
		var blocks []anthropic.BetaContentBlockParamUnion
		for _, r := range results {
			blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.ID, r.Text, r.IsError))
		}
		c.msgs = append(c.msgs, anthropic.NewBetaUserMessage(blocks...))
	} else {
		c.msgs = append(c.msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userText)))
	}
	params := anthropic.BetaMessageNewParams{
		Model:        c.model,
		MaxTokens:    16000,
		System:       []anthropic.BetaTextBlockParam{{Text: SystemPrompt}},
		Messages:     c.msgs,
		Tools:        c.tools,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortHigh},
	}
	if c.fallback {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	resp, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil && c.fallback {
		var ae *anthropic.Error
		if errors.As(err, &ae) && ae.StatusCode == 400 && strings.Contains(strings.ToLower(err.Error()), "fallback") {
			// this endpoint or model does not take the fallbacks field: carry on without it
			c.fallback = false
			params.Betas, params.Fallbacks = nil, anthropic.BetaFallbacksParamUnion{}
			resp, err = c.client.Beta.Messages.New(ctx, params)
		}
	}
	if err != nil {
		c.msgs = c.msgs[:len(c.msgs)-1]
		return nil, claudeErr(err)
	}
	// keep the full assistant turn (including thinking blocks) for the next request
	c.msgs = append(c.msgs, resp.ToParam())
	rep := &Reply{}
	var text []string
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			if strings.TrimSpace(v.Text) != "" {
				text = append(text, v.Text)
			}
		case anthropic.BetaToolUseBlock:
			rep.Calls = append(rep.Calls, ToolCall{ID: v.ID, Name: v.Name, Args: json.RawMessage(v.JSON.Input.Raw())})
		}
	}
	rep.Text = strings.Join(text, "\n\n")
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		rep.Calls = nil
		rep.Note = "The model declined this request"
		if resp.StopDetails.Explanation != "" {
			rep.Note += ": " + resp.StopDetails.Explanation
		}
	case anthropic.BetaStopReasonMaxTokens:
		rep.Calls = nil
		rep.Note = "The answer was cut off at the length limit. Ask it to continue."
	case anthropic.BetaStopReasonToolUse:
	default:
		rep.Calls = nil
	}
	return rep, nil
}

func claudeErr(err error) error {
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case 401, 403:
			return fmt.Errorf("Claude rejected the API key (HTTP %d). Check the key in Settings.", ae.StatusCode)
		case 404:
			return fmt.Errorf("Claude does not know this model name (HTTP 404). Check the model in Settings.")
		case 429:
			return fmt.Errorf("Claude rate limit reached (HTTP 429). Wait a minute and send again.")
		case 529, 500, 502, 503:
			return fmt.Errorf("Claude is overloaded or unavailable (HTTP %d). Try again shortly.", ae.StatusCode)
		}
		return fmt.Errorf("Claude API error (HTTP %d): %s", ae.StatusCode, short(ae.Error()))
	}
	return fmt.Errorf("cannot reach Claude: %s", short(err.Error()))
}

func short(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
