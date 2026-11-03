package agent_test

import (
	"context"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func TestContextPrependedToFirstUserMessageOnly(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("one"), fake.Text("two"))
	a := agent.New(agent.Config{
		Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)),
		Model:  "m", System: "sys", Context: "<environment>cwd</environment>",
	})
	a.Run(context.Background(), "first", nil)
	a.Run(context.Background(), "second", nil)

	hist := a.History()
	if len(hist[0].Blocks) != 2 || hist[0].Blocks[0].Text != "<environment>cwd</environment>" || hist[0].Blocks[1].Text != "first" {
		t.Errorf("first user message blocks = %+v", hist[0].Blocks)
	}
	if len(hist[2].Blocks) != 1 || hist[2].Blocks[0].Text != "second" {
		t.Errorf("later user messages must not repeat the context: %+v", hist[2].Blocks)
	}
	msgs, _ := request(t, reqs()[1])
	if parts, ok := msgs[1]["content"].([]any); !ok || len(parts) != 2 {
		t.Errorf("first user message should be sent as two text parts; got %v", msgs[1]["content"])
	}
}
