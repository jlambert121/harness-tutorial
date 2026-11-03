package agent_test

import (
	"context"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func TestTransformAppliedToRequestNotHistory(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("ok"))
	var events []agent.Event
	a := agent.New(agent.Config{
		Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m",
		Transform: func(system string, tools []llm.ToolSpec, msgs []llm.Message) ([]llm.Message, map[string]any) {
			out := append([]llm.Message{llm.UserText("injected")}, msgs...)
			return out, map[string]any{"estimated": 42}
		},
	})
	a.Run(context.Background(), "hello", func(e agent.Event) { events = append(events, e) })
	msgs, _ := request(t, reqs()[0])
	if len(msgs) != 2 || msgs[0]["content"] != "injected" {
		t.Errorf("request = %v", msgs)
	}
	if h := a.History(); len(h) != 2 || h[0].Text() != "hello" {
		t.Errorf("history must keep the untransformed messages: %+v", h)
	}
	var info map[string]any
	for _, e := range events {
		if e.Kind == agent.ContextInfo {
			info = e.Data
		}
	}
	if info["estimated"] != 42 {
		t.Errorf("context event data = %v", info)
	}
}
