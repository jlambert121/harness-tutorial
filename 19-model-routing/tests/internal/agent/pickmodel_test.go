package agent_test

import (
	"context"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/route"
	"harness/internal/tools"
)

func TestPickModelPerStep(t *testing.T) {
	url, reqs := fake.NewServer(t,
		fake.Turn{Thinking: "plan", Calls: []llm.ToolCall{fake.Call("c1", "read", map[string]string{"path": "x.txt"})}},
		fake.Text("done"),
	)
	reg := tools.NewRegistry(&tools.Env{Root: t.TempDir()})
	reg.Register(tools.Read())
	var events []agent.Event
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "small", Tools: reg,
		PickModel: func(step int, h []llm.Message) string {
			if step == 1 {
				return "small"
			}
			return "main"
		},
		PrepareForModel: route.TransformForModel,
		Fallbacks:       []string{"backup"},
	})
	a.Run(context.Background(), "go", func(e agent.Event) { events = append(events, e) })
	_, b1 := request(t, reqs()[0])
	_, b2 := request(t, reqs()[1])
	if b1["model"] != "small" || b2["model"] != "main" || a.Model() != "main" {
		t.Errorf("models: %v then %v", b1["model"], b2["model"])
	}
	if strings.Contains(string(reqs()[1]), "reasoning_details") {
		t.Error("thinking from the small model must not be sent to the main model")
	}
	if !strings.Contains(string(reqs()[0]), `"backup"`) {
		t.Error("fallback models must be sent with every request")
	}
	var change *agent.Event
	for i := range events {
		if events[i].Kind == agent.ModelChange {
			change = &events[i]
		}
	}
	if change == nil || change.Data["from"] != "small" || change.Data["to"] != "main" {
		t.Errorf("model_change event = %+v", change)
	}
}
