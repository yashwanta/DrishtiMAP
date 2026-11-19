package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"drishtimap/app"
)

// Test sends one short request to a model connection, with the tools attached exactly as a real
// conversation would, and reports what came back. It proves the address, the key, the model name
// and tool support in one go.
func Test(ctx context.Context, p app.Provider, key string) (string, error) {
	if strings.TrimSpace(p.Model) == "" {
		return "", fmt.Errorf("enter a model name first")
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return "", fmt.Errorf("enter the address first")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var s session
	if p.Kind == "anthropic" {
		s = newClaude(p, key)
	} else {
		s = newOpenAI(p, key)
	}
	t0 := time.Now()
	r, err := s.step(ctx, "Connection test from the settings window. Do not call any tool. Reply with the single word: connected", nil)
	if err != nil {
		return "", err
	}
	ans := strings.TrimSpace(r.Text)
	if len(ans) > 80 {
		ans = ans[:80] + "…"
	}
	if ans == "" && len(r.Calls) > 0 {
		ans = "(the model asked for tool " + r.Calls[0].Name + ")"
	}
	return fmt.Sprintf("Connected to %s in %.1f s. The model answered: %s", p.Model, time.Since(t0).Seconds(), ans), nil
}
