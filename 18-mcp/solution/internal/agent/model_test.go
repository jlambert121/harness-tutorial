package agent_test

import (
	"context"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func TestSetModel(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("a"), fake.Text("b"))
	a := agent.New(agent.Config{Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "first"})
	a.Run(context.Background(), "q1", nil)
	a.SetModel("second")
	a.Run(context.Background(), "q2", nil)
	_, b1 := request(t, reqs()[0])
	_, b2 := request(t, reqs()[1])
	if b1["model"] != "first" || b2["model"] != "second" || a.Model() != "second" {
		t.Errorf("models = %v, %v, Model()=%s", b1["model"], b2["model"], a.Model())
	}
}
