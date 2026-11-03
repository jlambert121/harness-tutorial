package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/tools"
)

func steerAgent(t *testing.T, turns ...fake.Turn) (*agent.Agent, func() []json.RawMessage, *[]agent.Event) {
	url, reqs := fake.NewServer(t, turns...)
	reg := tools.NewRegistry(&tools.Env{Root: t.TempDir()})
	var a *agent.Agent
	reg.Register(tools.Func{
		Def: llm.ToolSpec{Name: "work", Schema: json.RawMessage(`{"type":"object"}`)},
		Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
			a.Steer("also check the README")
			return tools.Result{Content: "worked"}
		},
	})
	a = agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", Tools: reg})
	var events []agent.Event
	return a, reqs, &events
}

func TestSteerInjectedAfterToolBatch(t *testing.T) {
	a, reqs, events := steerAgent(t, fake.Tools(fake.Call("c1", "work", map[string]any{})), fake.Text("done"))
	if err := a.Run(context.Background(), "do the work", func(e agent.Event) { *events = append(*events, e) }); err != nil {
		t.Fatal(err)
	}
	msgs, _ := request(t, reqs()[1])
	n := len(msgs)
	if msgs[n-2]["role"] != "tool" || msgs[n-1]["role"] != "user" || msgs[n-1]["content"] != "also check the README" {
		t.Errorf("steering message must follow the tool results in the next request; last two = %v, %v", msgs[n-2], msgs[n-1])
	}
	var steered bool
	for _, e := range *events {
		if e.Kind == agent.Steer && e.Text == "also check the README" {
			steered = true
		}
	}
	if !steered {
		t.Error("no steer event emitted")
	}
}

func TestSteerQueuedAfterFinalAnswerContinues(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("first answer"), fake.Text("second answer"))
	var a *agent.Agent
	a = agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m"})
	steered := false
	a.Run(context.Background(), "q", func(e agent.Event) {
		if e.Kind == agent.TextDelta && !steered {
			steered = true
			a.Steer("one more thing")
		}
	})
	if n := len(reqs()); n != 2 {
		t.Fatalf("a message queued while the model was answering must start another turn; requests = %d", n)
	}
	hist := a.History()
	if hist[len(hist)-1].Text() != "second answer" || hist[len(hist)-2].Text() != "one more thing" {
		t.Errorf("history tail = %+v", hist[len(hist)-2:])
	}
}
