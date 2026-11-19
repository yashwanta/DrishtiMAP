package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"drishtimap/app"
)

// The assistant is tested against mock servers that speak the two wire formats. What matters:
// the request carries the system prompt and the tools, a tool request is executed, its SUMMARY
// (and nothing else) goes back in the right shape, and the final text reaches the chat.

func newApp(t *testing.T) *app.App {
	t.Setenv("APPDATA", t.TempDir()) // keep the user's real settings untouched
	ws := t.TempDir()
	a, err := app.New(ws)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func waitDone(t *testing.T, c *Chat) []Event {
	for i := 0; i < 200; i++ {
		time.Sleep(50 * time.Millisecond)
		if ev, running := c.Events(0); !running {
			return ev
		}
	}
	t.Fatal("chat did not finish")
	return nil
}

func TestClaudeToolLoop(t *testing.T) {
	a := newApp(t)
	var mu sync.Mutex
	var bodies []map[string]any
	var betas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		json.Unmarshal(raw, &b)
		mu.Lock()
		bodies = append(bodies, b)
		betas = append(betas, r.Header.Get("anthropic-beta"))
		n := len(bodies)
		mu.Unlock()
		// the current SDK sends the key as "Authorization: Bearer"; older ones used x-api-key
		if r.URL.Path != "/v1/messages" || (r.Header.Get("Authorization") != "Bearer sk-test" && r.Header.Get("x-api-key") != "sk-test") {
			http.Error(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`, 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"Let me look at the workspace."},{"type":"tool_use","id":"toolu_1","name":"list_workspace","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
			return
		}
		io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"The workspace is empty: no maps and no recordings."}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":9}}`)
	}))
	defer srv.Close()
	if err := a.SaveProvider(app.Provider{ID: "claude", Name: "Claude", Kind: "anthropic", BaseURL: srv.URL, Model: "claude-opus-5-5"}, "sk-test", true); err != nil {
		t.Fatal(err)
	}
	c := New(a)
	if err := c.Send("What is in the workspace?"); err != nil {
		t.Fatal(err)
	}
	ev := waitDone(t, c)
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.Type)
	}
	t.Log(kinds)
	if strings.Join(kinds, ",") != "user,text,tool,result,text" {
		for _, e := range ev {
			t.Logf("%s: %s", e.Type, e.Text)
		}
		t.Fatalf("unexpected chat flow: %v", kinds)
	}
	if !strings.Contains(ev[4].Text, "workspace is empty") || !strings.Contains(ev[3].Text, "maps:") {
		t.Errorf("final text or tool result missing")
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	b1, b2 := bodies[0], bodies[1]
	if b1["model"] != "claude-opus-5-5" || len(b1["tools"].([]any)) != len(app.Tools) {
		t.Errorf("first request: model or tools wrong")
	}
	sys, _ := json.Marshal(b1["system"])
	if !strings.Contains(string(sys), "DRISHTI Map") {
		t.Errorf("system prompt missing")
	}
	if b1["fallbacks"] != "default" || !strings.Contains(betas[0], "server-side-fallback-2026-07-01") {
		t.Errorf("refusal fallbacks not requested: fallbacks=%v beta=%q", b1["fallbacks"], betas[0])
	}
	if oc, _ := b1["output_config"].(map[string]any); oc == nil || oc["effort"] != "high" {
		t.Errorf("effort not set: %v", b1["output_config"])
	}
	if _, has := b1["thinking"]; has {
		t.Errorf("thinking must be left unset for this model")
	}
	msgs := b2["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("second request should carry user, assistant, tool results; got %d messages", len(msgs))
	}
	as, _ := json.Marshal(msgs[1])
	if !strings.Contains(string(as), `"thinking"`) || !strings.Contains(string(as), `"tool_use"`) {
		t.Errorf("assistant turn must be sent back whole (thinking + tool_use): %s", as)
	}
	last := msgs[2].(map[string]any)
	blocks := last["content"].([]any)
	tr := blocks[0].(map[string]any)
	if last["role"] != "user" || len(blocks) != 1 || tr["type"] != "tool_result" || tr["tool_use_id"] != "toolu_1" {
		t.Errorf("tool result shape wrong: %v", last)
	}
	out, _ := json.Marshal(tr["content"])
	if !strings.Contains(string(out), "workspace:") {
		t.Errorf("tool result should be the tool's text summary: %s", out)
	}

	// wrong key -> clear message, no crash
	a.SaveProvider(app.Provider{ID: "claude", Name: "Claude", Kind: "anthropic", BaseURL: srv.URL, Model: "claude-opus-5-5"}, "sk-wrong", true)
	c2 := New(a)
	c2.Send("hello")
	ev = waitDone(t, c2)
	if ev[len(ev)-1].Type != "error" || !strings.Contains(ev[len(ev)-1].Text, "API key") {
		t.Errorf("bad key should give a clear error, got %+v", ev[len(ev)-1])
	}
}

func TestClaudeRefusal(t *testing.T) {
	a := newApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"declined by policy"},"usage":{"input_tokens":10,"output_tokens":0}}`)
	}))
	defer srv.Close()
	a.SaveProvider(app.Provider{ID: "claude", Name: "Claude", Kind: "anthropic", BaseURL: srv.URL, Model: "claude-opus-5-5"}, "k", true)
	c := New(a)
	c.Send("x")
	ev := waitDone(t, c)
	if e := ev[len(ev)-1]; e.Type != "note" || !strings.Contains(e.Text, "declined") {
		t.Errorf("refusal should be shown as a note, got %+v", e)
	}
}

func TestOpenAIToolLoop(t *testing.T) {
	a := newApp(t)
	var bodies []map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, `{"error":{"message":"no such route"}}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			// arguments as a JSON string (OpenAI) for one call and as an object (some local servers) for the other
			io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"list_workspace","arguments":"{}"}},{"id":"","type":"function","function":{"name":"map_info","arguments":{"map":"NOPE"}}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Nothing in the workspace; NOPE does not exist."}}]}`)
	}))
	defer srv.Close()
	a.SaveProvider(app.Provider{ID: "local", Name: "Local", Kind: "openai", BaseURL: srv.URL + "/v1", Model: "some-model"}, "", true)
	c := New(a)
	if err := c.Send("check"); err != nil {
		t.Fatal(err)
	}
	ev := waitDone(t, c)
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.Type)
	}
	if strings.Join(kinds, ",") != "user,tool,result,tool,result,text" {
		for _, e := range ev {
			t.Logf("%s: %s", e.Type, e.Text)
		}
		t.Fatalf("unexpected chat flow: %v", kinds)
	}
	if !strings.Contains(ev[4].Text, "ERROR") || !strings.Contains(ev[4].Text, "not found") {
		t.Errorf("a failing tool must come back as an error text, got %q", ev[4].Text)
	}
	if auth != "" {
		t.Errorf("no key was set, so no Authorization header may be sent (got %q)", auth)
	}
	msgs := bodies[1]["messages"].([]any)
	if len(msgs) != 5 { // system, user, assistant(tool_calls), tool, tool
		t.Fatalf("second request should have 5 messages, got %d", len(msgs))
	}
	if m := msgs[0].(map[string]any); m["role"] != "system" || !strings.Contains(m["content"].(string), "DRISHTI Map") {
		t.Errorf("system prompt missing")
	}
	t3, t4 := msgs[3].(map[string]any), msgs[4].(map[string]any)
	if t3["role"] != "tool" || t3["tool_call_id"] != "call_a" || t4["role"] != "tool" || t4["tool_call_id"] == "" {
		t.Errorf("tool messages wrong: %v %v", t3, t4)
	}
	if len(bodies[0]["tools"].([]any)) != len(app.Tools) {
		t.Errorf("tools not sent")
	}
}

// Keys are stored encrypted and never returned to the window.
func TestKeyStorage(t *testing.T) {
	a := newApp(t)
	a.SaveProvider(app.Provider{ID: "p1", Name: "P", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "m"}, "sk-secret-123", true)
	cfg, _ := os.ReadFile(os.Getenv("APPDATA") + `\DRISHTI Map\config.json`)
	if strings.Contains(string(cfg), "sk-secret-123") || !strings.Contains(string(cfg), "keyEnc") {
		t.Errorf("the key must be stored encrypted")
	}
	ps, _ := a.ProvidersView()
	for _, p := range ps {
		if p.KeyEnc != "" {
			t.Errorf("the encrypted key must not be sent to the window")
		}
		if p.ID == "p1" && !p.HasKey {
			t.Errorf("hasKey should be true")
		}
	}
	if _, key, err := a.ActiveProvider(); err != nil || key != "sk-secret-123" {
		t.Errorf("stored key should decrypt for this user: %q %v", key, err)
	}
}
