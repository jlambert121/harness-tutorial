package agent_test

import (
	"context"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func compactingAgent(t *testing.T, should func([]llm.Message) bool, turns ...fake.Turn) (*agent.Agent, func() [][]map[string]any, *[]agent.Event, *[][]llm.Message) {
	url, reqs := fake.NewServer(t, turns...)
	var events []agent.Event
	var saved [][]llm.Message
	a := agent.New(agent.Config{
		Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), Model: "m", System: "sys",
		ShouldCompact: func(system string, tools []llm.ToolSpec, msgs []llm.Message) bool { return should(msgs) },
		Compact: func(ctx context.Context, system string, tools []llm.ToolSpec, msgs []llm.Message, focus string) ([]llm.Message, error) {
			return []llm.Message{llm.UserText("<conversation-summary>earlier work</conversation-summary>"), msgs[len(msgs)-1]}, nil
		},
		IsOverflow: func(err string) bool { return strings.Contains(err, "too long") },
		OnCompact:  func(h []llm.Message) { saved = append(saved, h) },
	})
	all := func() [][]map[string]any {
		var out [][]map[string]any
		for _, r := range reqs() {
			m, _ := request(t, r)
			out = append(out, m)
		}
		return out
	}
	return a, all, &events, &saved
}

func TestCompactionAtThreshold(t *testing.T) {
	first := true
	a, reqs, events, saved := compactingAgent(t, func(m []llm.Message) bool {
		if len(m) > 2 && first {
			first = false
			return true
		}
		return false
	}, fake.Text("a1"), fake.Text("a2"))
	a.Run(context.Background(), "q1", nil)
	a.Run(context.Background(), "q2", func(e agent.Event) { *events = append(*events, e) })
	second := reqs()[1]
	if len(second) != 3 || !strings.Contains(second[1]["content"].(string), "earlier work") || second[2]["content"] != "q2" {
		t.Errorf("request after compaction = %v", second)
	}
	if len(a.History()) != 3 || len(*saved) != 1 {
		t.Errorf("history = %d messages, OnCompact calls = %d", len(a.History()), len(*saved))
	}
	var ev *agent.Event
	for i := range *events {
		if (*events)[i].Kind == agent.Compaction {
			ev = &(*events)[i]
		}
	}
	if ev == nil || ev.Data["reason"] != "threshold" || ev.Data["before"] != 3 || ev.Data["after"] != 2 {
		t.Errorf("compaction event = %+v", ev)
	}
}

func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	overflow := fake.Turn{Status: 400, Body: `{"error":{"message":"prompt is too long: 300000 tokens > 200000 maximum"}}`}
	a, reqs, _, _ := compactingAgent(t, func([]llm.Message) bool { return false }, overflow, fake.Text("recovered"))
	if err := a.Run(context.Background(), "big question", nil); err != nil {
		t.Fatalf("overflow should be recovered by compacting: %v", err)
	}
	if n := len(reqs()); n != 2 {
		t.Errorf("requests = %d, want the failed one plus one retry", n)
	}

	b, _, _, _ := compactingAgent(t, func([]llm.Message) bool { return false }, overflow, overflow)
	if err := b.Run(context.Background(), "still too big", nil); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("a second overflow after compacting must surface: %v", err)
	}
}
