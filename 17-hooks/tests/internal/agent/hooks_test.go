package agent_test

import (
	"context"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func TestPromptHookBlocksAndAddsContext(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("ok"))
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m",
		OnPrompt: func(ctx context.Context, input string) (bool, string, string) {
			if strings.Contains(input, "password") {
				return true, "prompts must not contain secrets", ""
			}
			return false, "", "ticket: ABC-123"
		}})
	if err := a.Run(context.Background(), "my password is hunter2", nil); err == nil || !strings.Contains(err.Error(), "secrets") {
		t.Errorf("blocked prompt err = %v", err)
	}
	if len(reqs()) != 0 || len(a.History()) != 0 {
		t.Error("a blocked prompt must not reach the model or the history")
	}
	a.Run(context.Background(), "fix the bug", nil)
	if !strings.Contains(string(reqs()[0]), "ticket: ABC-123") {
		t.Errorf("hook context should be added to the prompt: %s", reqs()[0])
	}
}

func TestStopHookContinuesLoopCapped(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("done 1"), fake.Text("done 2"), fake.Text("done 3"), fake.Text("done 4"), fake.Text("done 5"))
	calls := 0
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m",
		OnStop: func(ctx context.Context) string {
			calls++
			return "tests still fail: TestX"
		}})
	if err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(reqs()); n != 4 {
		t.Errorf("requests = %d; a stop hook may send the model back at most 3 times", n)
	}
	h := a.History()
	if !strings.Contains(h[2].Text(), "tests still fail") {
		t.Errorf("stop hook feedback should arrive as a user message: %+v", h[2])
	}
}
