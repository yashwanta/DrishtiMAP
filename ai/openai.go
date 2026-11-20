package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"drishtimap/app"
)

// openAI talks to any server that implements the OpenAI chat-completions API with function calling:
// OpenAI, Azure OpenAI (v1 endpoint), Google Gemini's OpenAI endpoint, Mistral, Groq, OpenRouter,
// LM Studio, vLLM, and Ollama on this PC (http://127.0.0.1:11434/v1).
type openAI struct {
	base, key, model string
	msgs             []map[string]any
	tools            []map[string]any
	http             *http.Client
}

func newOpenAI(p app.Provider, key string) *openAI {
	o := &openAI{base: strings.TrimRight(p.BaseURL, "/"), key: key, model: p.Model, http: &http.Client{Timeout: 15 * time.Minute}}
	o.msgs = []map[string]any{{"role": "system", "content": SystemPrompt}}
	for _, t := range app.Tools {
		o.tools = append(o.tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Desc, "parameters": json.RawMessage(strings.Replace(string(t.Params), `,"required":[]`, "", 1))}})
	}
	return o
}

func (o *openAI) step(ctx context.Context, userText string, results []ToolResult) (*Reply, error) {
	n0 := len(o.msgs)
	if len(results) > 0 {
		for _, r := range results {
			o.msgs = append(o.msgs, map[string]any{"role": "tool", "tool_call_id": r.ID, "content": r.Text})
		}
	} else {
		o.msgs = append(o.msgs, map[string]any{"role": "user", "content": userText})
	}
	payload := map[string]any{"model": o.model, "messages": o.msgs, "stream": false}
	if len(o.tools) > 0 {
		payload["tools"] = o.tools
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", o.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		o.msgs = o.msgs[:n0]
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		o.msgs = o.msgs[:n0]
		return nil, fmt.Errorf("cannot reach the model at %s: %s", o.base, short(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		o.msgs = o.msgs[:n0]
		msg := short(strings.TrimSpace(string(raw)))
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = short(e.Error.Message)
		}
		switch resp.StatusCode {
		case 401, 403:
			return nil, fmt.Errorf("the model server rejected the API key (HTTP %d). Check the key in Settings.", resp.StatusCode)
		case 404:
			return nil, fmt.Errorf("the model server does not know this address or model (HTTP 404): %s", msg)
		case 429:
			return nil, fmt.Errorf("rate limit reached (HTTP 429). Wait a minute and send again.")
		}
		return nil, fmt.Errorf("model server error (HTTP %d): %s", resp.StatusCode, msg)
	}
	var out struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		o.msgs = o.msgs[:n0]
		return nil, fmt.Errorf("the model server sent an answer this app cannot read: %s", short(string(raw)))
	}
	ch := out.Choices[0]
	rep := &Reply{}
	if ch.Message.Content != nil {
		rep.Text = strings.TrimSpace(*ch.Message.Content)
	}
	am := map[string]any{"role": "assistant", "content": rep.Text}
	var calls []map[string]any
	for i, tc := range ch.Message.ToolCalls {
		// arguments are a JSON string on OpenAI; some servers send a JSON object
		args := tc.Function.Arguments
		var s string
		if json.Unmarshal(args, &s) == nil {
			args = json.RawMessage(s)
		}
		if len(bytes.TrimSpace(args)) == 0 {
			args = json.RawMessage("{}")
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", len(o.msgs), i)
		}
		rep.Calls = append(rep.Calls, ToolCall{ID: id, Name: tc.Function.Name, Args: args})
		calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": tc.Function.Name, "arguments": string(args)}})
	}
	if len(calls) > 0 {
		am["tool_calls"] = calls
	}
	o.msgs = append(o.msgs, am)
	if ch.FinishReason == "length" {
		rep.Calls = nil
		rep.Note = "The answer was cut off at the length limit. Ask it to continue."
	}
	if ch.FinishReason == "content_filter" {
		rep.Calls = nil
		rep.Note = "The model's content filter stopped this answer."
	}
	if rep.Text == "" && len(rep.Calls) == 0 && rep.Note == "" {
		rep.Note = "The model returned an empty answer. Some small local models cannot use tools; try a larger model."
	}
	return rep, nil
}
