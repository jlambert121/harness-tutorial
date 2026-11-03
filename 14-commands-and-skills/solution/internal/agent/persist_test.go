package agent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/tools"
)

func TestPersistSeesEveryHistoryAppend(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Tools(fake.Call("c1", "work", map[string]any{})), fake.Text("done"))
	reg := tools.NewRegistry(&tools.Env{Root: t.TempDir()})
	var a *agent.Agent
	reg.Register(tools.Func{
		Def: llm.ToolSpec{Name: "work", Schema: json.RawMessage(`{"type":"object"}`)},
		Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
			a.Steer("and this")
			return tools.Result{Content: "ok"}
		},
	})
	var persisted []llm.Message
	var started []string
	a = agent.New(agent.Config{
		Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", Tools: reg, Context: "ctx",
		Persist:    func(m llm.Message) { persisted = append(persisted, m) },
		OnRunStart: func(ctx context.Context, input string) { started = append(started, input) },
	})
	if err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, a.History()) {
		t.Errorf("persisted %d messages, history has %d; every append must be persisted in order", len(persisted), len(a.History()))
	}
	if len(started) != 1 || started[0] != "go" {
		t.Errorf("OnRunStart calls = %v", started)
	}
}

func TestLoadRestoresHistory(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("resumed"))
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", Context: "ctx"})
	a.Load([]llm.Message{llm.UserText("earlier question"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Text: "earlier answer"}}}})
	a.Run(context.Background(), "follow-up", nil)
	msgs, _ := request(t, reqs()[0])
	if len(msgs) != 3 || msgs[0]["content"] != "earlier question" || msgs[2]["content"] != "follow-up" {
		t.Errorf("request after Load = %v (context must not be re-added to a loaded conversation)", msgs)
	}
}
