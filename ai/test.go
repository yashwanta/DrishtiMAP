package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"drishtimap/app"
)

const testPrompt = "Connection test from the settings window. Do not call any tool. Reply with the single word: connected"

// tryModel sends one short request. withTools attaches the tools exactly as a real conversation would.
func tryModel(ctx context.Context, p app.Provider, key, model string, withTools bool) (string, time.Duration, error) {
	p.Model = model
	var s session
	if p.Kind == "anthropic" {
		s = newClaude(p, key)
	} else {
		o := newOpenAI(p, key)
		if !withTools {
			o.tools = nil
		}
		s = o
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	t0 := time.Now()
	r, err := s.step(c, testPrompt, nil)
	if err != nil {
		return "", time.Since(t0), err
	}
	ans := strings.TrimSpace(r.Text)
	if len(ans) > 80 {
		ans = ans[:80] + "…"
	}
	if ans == "" && len(r.Calls) > 0 {
		ans = "(the model asked for tool " + r.Calls[0].Name + ")"
	}
	return ans, time.Since(t0), nil
}

// Test sends one short request to a model connection, with the tools attached exactly as a real
// conversation would, and reports what came back. It proves the address, the key, the model name
// and tool support in one go.
//
// When the chosen model fails, it finds out why instead of only passing the error on: the same
// request without tools tells a model that cannot take tools from a model that is down, and the
// other models of the connection are tried so the user knows which ones work right now.
func Test(ctx context.Context, p app.Provider, key string) (string, error) {
	if strings.TrimSpace(p.Model) == "" {
		return "", fmt.Errorf("enter a model name first")
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return "", fmt.Errorf("enter the address first")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	ans, d, err := tryModel(ctx, p, key, p.Model, true)
	if err == nil {
		return fmt.Sprintf("Connected to %s in %.1f s. The model answered: %s", p.Model, d.Seconds(), ans), nil
	}
	first := err.Error()
	// a wrong key or address is the same for every model: say it and stop
	if strings.Contains(first, "rejected the API key") || strings.Contains(first, "cannot reach the model") || p.Kind == "anthropic" {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s did not answer: %s. ", p.Model, strings.TrimRight(first, ". "))
	if _, _, err2 := tryModel(ctx, p, key, p.Model, false); err2 == nil {
		b.WriteString("The same model DOES answer when no tools are attached, so it is reachable but does not accept tool definitions. The assistant needs a model that can call tools. ")
	} else {
		b.WriteString("It fails the same way without tools, so the fault is on the server's side for this model (it is down, renamed, or not available to this key), not in the request. ")
	}
	var good, bad []string
	for _, m := range p.Models {
		if m == p.Model || ctx.Err() != nil {
			continue
		}
		if _, d, err := tryModel(ctx, p, key, m, true); err == nil {
			good = append(good, fmt.Sprintf("%s (%.1f s)", m, d.Seconds()))
		} else {
			bad = append(bad, m)
		}
	}
	switch {
	case len(good) > 0:
		fmt.Fprintf(&b, "Models on this connection that answer WITH tools right now: %s. Put one of them in the Model field, or choose it in the Assistant's list.", strings.Join(good, ", "))
		if len(bad) > 0 {
			fmt.Fprintf(&b, " Not answering: %s.", strings.Join(bad, ", "))
		}
	case len(p.Models) > 1:
		b.WriteString("None of the other models on this connection answered either. The key and the address are accepted, so ask whoever runs the server which models this key may use.")
	default:
		b.WriteString("Try another model name.")
	}
	return "", fmt.Errorf("%s", b.String())
}
