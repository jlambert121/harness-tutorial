package route_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/route"
)

func assistant(model string, blocks ...llm.Block) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Model: model, Blocks: blocks, Stop: llm.StopToolUse}
}

func TestTransformDropsForeignThinking(t *testing.T) {
	msgs := []llm.Message{
		llm.UserText("q"),
		assistant("anthropic/claude-haiku-5.5", llm.Block{Thinking: &llm.Thinking{Raw: json.RawMessage(`[{"signature":"x"}]`)}}, llm.Block{Text: "hmm"}),
		assistant("anthropic/claude-sonnet-5.5", llm.Block{Thinking: &llm.Thinking{Raw: json.RawMessage(`[{"signature":"y"}]`)}}, llm.Block{Text: "ok"}),
	}
	out := route.TransformForModel(msgs, "anthropic/claude-sonnet-5.5")
	if out[1].Blocks[0].Thinking != nil || out[1].Text() != "hmm" {
		t.Errorf("thinking signed by another model must be dropped (it would be rejected): %+v", out[1].Blocks)
	}
	if out[2].Blocks[0].Thinking == nil {
		t.Error("the target model's own thinking must be kept")
	}
	if msgs[1].Blocks[0].Thinking == nil {
		t.Error("input must not be modified")
	}
}

func TestTransformNormalizesIDsConsistently(t *testing.T) {
	long := "fc_" + strings.Repeat("a|b", 40)
	msgs := []llm.Message{
		llm.UserText("q"),
		assistant("openai/gpt-6-luna", llm.Block{ToolCall: &llm.ToolCall{ID: long, Name: "read"}}),
		{Role: llm.RoleTool, Result: &llm.ToolResult{CallID: long, Name: "read", Content: "x"}},
	}
	out := route.TransformForModel(msgs, "anthropic/claude-haiku-5.5")
	id := out[1].ToolCalls()[0].ID
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(id) {
		t.Errorf("id %q does not satisfy Anthropic's pattern", id)
	}
	if out[2].Result.CallID != id {
		t.Errorf("the result must use the same rewritten id: %q vs %q", out[2].Result.CallID, id)
	}
	ok := []llm.Message{assistant("m", llm.Block{ToolCall: &llm.ToolCall{ID: "toolu_01abc", Name: "x"}})}
	if route.TransformForModel(ok, "other")[0].ToolCalls()[0].ID != "toolu_01abc" {
		t.Error("valid ids must not change")
	}
}

func TestTransformDropsFailedTurns(t *testing.T) {
	msgs := []llm.Message{
		llm.UserText("q"),
		{Role: llm.RoleAssistant, Model: "a", Stop: llm.StopAborted, Blocks: []llm.Block{{Text: "partial"}}},
		llm.UserText("again"),
	}
	if out := route.TransformForModel(msgs, "b"); len(out) != 2 {
		t.Errorf("aborted partial answers are dropped when switching models: %d messages", len(out))
	}
}

func toolResult(isErr bool) llm.Message {
	return llm.Message{Role: llm.RoleTool, Result: &llm.ToolResult{CallID: "x", IsError: isErr}}
}

func TestEscalatingRouterNeverDowngrades(t *testing.T) {
	r := route.NewEscalating("small", "main", 3)
	hist := []llm.Message{llm.UserText("fix it")}
	if r.Pick(1, hist) != "small" {
		t.Fatal("starts small")
	}
	hist = append(hist, toolResult(true), toolResult(true))
	if r.Pick(2, hist) != "small" {
		t.Error("two failures are not enough")
	}
	hist = append(hist, toolResult(true))
	if r.Pick(3, hist) != "main" || !r.Escalated() {
		t.Error("three consecutive failed tool results escalate")
	}
	hist = append(hist, toolResult(false), toolResult(false))
	if r.Pick(4, hist) != "main" {
		t.Error("once escalated, stay: switching back would lose the main model's cache and thinking")
	}
}

func TestHeuristicRouter(t *testing.T) {
	r := route.Heuristic{Small: "small", Main: "main"}
	if r.Pick(1, []llm.Message{llm.UserText("rename Usr to User")}) != "small" {
		t.Error("a short mechanical request should go to the small model")
	}
	if r.Pick(1, []llm.Message{llm.UserText("Sum sometimes returns the wrong total under load; fix the race")}) != "main" {
		t.Error("concurrency and debugging words should go to the main model")
	}
	run := route.Heuristic{Small: "small", Main: "main"}
	run.Pick(1, []llm.Message{llm.UserText("rename Usr to User")})
	hist := []llm.Message{llm.UserText("rename Usr to User"), llm.UserText("also redesign the architecture of the store")}
	if run.Pick(5, hist) != "small" {
		t.Error("the choice is made at step 1 of a run; a later message in the same run must not flip it")
	}
}

func TestTitleUsesSmallModel(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("Fix pagination off-by-one\n"))
	title, err := route.Title(context.Background(), llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), "small-model", "Paginate returns the wrong items for pages after the first. Fix it.")
	if err != nil || title != "Fix pagination off-by-one" {
		t.Errorf("title = %q, %v", title, err)
	}
	var body map[string]any
	json.Unmarshal(reqs()[0], &body)
	if body["model"] != "small-model" || body["tools"] != nil {
		t.Errorf("title request = %v", body)
	}
}
