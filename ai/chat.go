// Package ai connects DRISHTI Map to a language model. The model can call the app's tools; it is
// sent only each tool's text summary, never map or laser files.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"drishtimap/app"
)

// ToolCall is a tool request from the model.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	ID      string
	Name    string
	Text    string
	IsError bool
}

// Reply is one model turn.
type Reply struct {
	Text  string
	Calls []ToolCall
	Note  string // e.g. refusal or cut-off explanation
}

// session keeps one provider's conversation in that provider's own format.
type session interface {
	// step sends the user's text (first call of a turn) or tool results (following calls).
	step(ctx context.Context, userText string, results []ToolResult) (*Reply, error)
}

// Event is one line of the chat as shown in the window.
type Event struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"` // user, text, tool, result, note, error
	Text string `json:"text"`
	Tool string `json:"tool,omitempty"`
}

// Chat is the assistant conversation.
type Chat struct {
	app     *app.App
	mu      sync.Mutex
	sess    session
	sessKey string
	events  []Event
	running bool
	cancel  context.CancelFunc
}

func New(a *app.App) *Chat { return &Chat{app: a} }

func (c *Chat) add(typ, text, tool string) {
	c.mu.Lock()
	c.events = append(c.events, Event{Seq: len(c.events) + 1, Type: typ, Text: text, Tool: tool})
	c.mu.Unlock()
}

// Events returns events after seq and whether the assistant is still working.
func (c *Chat) Events(after int) ([]Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if after < 0 || after > len(c.events) {
		after = 0
	}
	return append([]Event{}, c.events[after:]...), c.running
}

// Reset starts a new conversation.
func (c *Chat) Reset() {
	c.Stop()
	c.mu.Lock()
	c.sess, c.sessKey, c.events = nil, "", nil
	c.mu.Unlock()
}

// Stop interrupts the running turn.
func (c *Chat) Stop() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

const maxRounds = 30
const maxResult = 12000

// Send starts a turn with the user's message.
func (c *Chat) Send(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message")
	}
	p, key, err := c.app.ActiveProvider()
	if err != nil {
		return err
	}
	if p.Model == "" {
		return fmt.Errorf("no model name is set for %q; open Settings and enter one", p.Name)
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("the assistant is still working; wait or press Stop")
	}
	k := p.ID + "|" + p.Kind + "|" + p.BaseURL + "|" + p.Model
	if c.sess == nil || c.sessKey != k {
		if c.sess != nil {
			c.events = append(c.events, Event{Seq: len(c.events) + 1, Type: "note", Text: "Model changed to " + p.Name + " (" + p.Model + "). It starts without the earlier conversation."})
		}
		switch p.Kind {
		case "anthropic":
			c.sess = newClaude(p, key)
		default:
			c.sess = newOpenAI(p, key)
		}
		c.sessKey = k
	}
	sess := c.sess
	ctx, cancel := context.WithCancel(context.Background())
	c.running, c.cancel = true, cancel
	c.mu.Unlock()
	c.add("user", text, "")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.add("error", fmt.Sprintf("internal error: %v", r), "")
			}
			cancel()
			c.mu.Lock()
			c.running, c.cancel = false, nil
			c.mu.Unlock()
		}()
		user := text
		var results []ToolResult
		for round := 0; ; round++ {
			rep, err := sess.step(ctx, user, results)
			if ctx.Err() != nil {
				c.add("note", "Stopped.", "")
				// the provider conversation may now end on an unanswered tool request: start clean next time
				c.mu.Lock()
				c.sess, c.sessKey = nil, ""
				c.mu.Unlock()
				return
			}
			if err != nil {
				c.add("error", err.Error(), "")
				c.mu.Lock()
				c.sess, c.sessKey = nil, ""
				c.mu.Unlock()
				return
			}
			if rep.Text != "" {
				c.add("text", rep.Text, "")
			}
			if rep.Note != "" {
				c.add("note", rep.Note, "")
			}
			if len(rep.Calls) == 0 {
				return
			}
			if round >= maxRounds {
				c.add("note", fmt.Sprintf("Stopped after %d tool rounds. Ask it to continue if needed.", maxRounds), "")
				c.mu.Lock()
				c.sess, c.sessKey = nil, ""
				c.mu.Unlock()
				return
			}
			user, results = "", nil
			for _, call := range rep.Calls {
				c.add("tool", string(call.Args), call.Name)
				res, err := c.app.RunSync(ctx, call.Name, call.Args)
				tr := ToolResult{ID: call.ID, Name: call.Name}
				if err != nil {
					tr.Text, tr.IsError = "ERROR: "+err.Error(), true
				} else {
					tr.Text = res.Summary
					if len(tr.Text) > maxResult {
						tr.Text = tr.Text[:maxResult] + "\n... (cut: the full result is shown in the DRISHTI Map window)"
					}
				}
				c.add("result", tr.Text, call.Name)
				results = append(results, tr)
			}
		}
	}()
	return nil
}
