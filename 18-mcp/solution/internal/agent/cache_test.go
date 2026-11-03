package agent_test

import (
	"context"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func TestCacheOptionsReachRequest(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("ok"))
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", System: "sys",
		Cache: llm.CacheAuto, SessionID: "s-123"})
	a.Run(context.Background(), "hi", nil)
	_, body := request(t, reqs()[0])
	g := body["providerOptions"].(map[string]any)["gateway"].(map[string]any)
	if g["caching"] != "auto" || g["sessionId"] != "s-123" {
		t.Errorf("gateway options = %v", g)
	}
}

func TestRemindAddsToNextUserMessageOnly(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("one"), fake.Text("two"))
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", System: "stable system prompt"})
	a.Run(context.Background(), "first", nil)
	a.Remind("Plan mode is now active: do not edit files.")
	a.Run(context.Background(), "second", nil)

	msgs, _ := request(t, reqs()[1])
	if msgs[0]["content"] != "stable system prompt" {
		t.Errorf("a reminder must never change the system prompt (it would invalidate the cache): %v", msgs[0])
	}
	parts, ok := msgs[3]["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("second user message = %v", msgs[3])
	}
	reminder := parts[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(reminder, "<system-reminder>") || !strings.Contains(reminder, "Plan mode is now active") || parts[1].(map[string]any)["text"] != "second" {
		t.Errorf("parts = %v", parts)
	}
	if msgs[1]["content"] != "first" {
		t.Errorf("earlier messages must be unchanged: %v", msgs[1])
	}
}
