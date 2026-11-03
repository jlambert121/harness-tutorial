package ctxmgr_test

import (
	"strings"
	"testing"

	"harness/internal/ctxmgr"
	"harness/internal/llm"
)

func TestUsable(t *testing.T) {
	b := ctxmgr.NewBudget(llm.ModelInfo{ContextWindow: 200000, MaxTokens: 64000}, 0)
	if b.Window != 200000 || b.Reserve != 20000 || b.Usable() != 180000 {
		t.Errorf("budget = %+v usable=%d; reserve = min(20000, max output)", b, b.Usable())
	}
	small := ctxmgr.NewBudget(llm.ModelInfo{ContextWindow: 32000, MaxTokens: 4096}, 0)
	if small.Reserve != 4096 || small.Usable() != 27904 {
		t.Errorf("small = %+v", small)
	}
	override := ctxmgr.NewBudget(llm.ModelInfo{ContextWindow: 1000000, MaxTokens: 128000}, 30000)
	if override.Window != 30000 || override.Usable() != 22500 {
		t.Errorf("an explicit window overrides the catalog, reserving at most a quarter of it: %+v", override)
	}
}

func TestCalibratorConverges(t *testing.T) {
	var c ctxmgr.Calibrator
	if got := c.Estimate(strings.Repeat("x", 400)); got != 100 {
		t.Errorf("uncalibrated estimate = %d, want bytes/4 = 100", got)
	}
	for i := 0; i < 20; i++ {
		c.Observe(1000, 2000) // the provider keeps reporting twice our estimate
	}
	if r := c.Ratio(); r < 1.9 || r > 2.1 {
		t.Errorf("ratio = %.2f, want ≈2", r)
	}
	if got := c.Estimate(strings.Repeat("x", 400)); got < 190 || got > 210 {
		t.Errorf("calibrated estimate = %d", got)
	}
	c.Observe(0, 50) // ignored: no estimate to compare
	if r := c.Ratio(); r < 1.9 {
		t.Errorf("zero estimates must not skew the ratio: %.2f", r)
	}
}

func toolTurn(id, content string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{ToolCall: &llm.ToolCall{ID: id, Name: "read", Args: []byte(`{"path":"f"}`)}}}},
		{Role: llm.RoleTool, Result: &llm.ToolResult{CallID: id, Name: "read", Content: content}},
	}
}

func TestPruneKeepsRecentAndPairs(t *testing.T) {
	big := strings.Repeat("y", 40000) // ≈10k tokens each
	var msgs []llm.Message
	msgs = append(msgs, llm.UserText("first task"))
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		msgs = append(msgs, toolTurn(id, big)...)
	}
	msgs = append(msgs, llm.UserText("second task"))
	msgs = append(msgs, toolTurn("g", big)...)
	msgs = append(msgs, llm.UserText("third task"))
	msgs = append(msgs, toolTurn("h", big)...)

	out, cleared := ctxmgr.Prune(msgs, 25000)
	if len(out) != len(msgs) {
		t.Fatalf("Prune must not remove messages (that would orphan tool calls): %d → %d", len(msgs), len(out))
	}
	content := map[string]string{}
	for _, m := range out {
		if m.Result != nil {
			content[m.Result.CallID] = m.Result.Content
		}
	}
	if content["g"] != big || content["h"] != big {
		t.Error("tool results in the last two user turns must be kept")
	}
	if content["f"] != big || content["e"] != big {
		t.Error("the most recent ~25k tokens of older tool output must be kept")
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if content[id] != ctxmgr.ClearedMarker {
			t.Errorf("result %s should be cleared, got %d bytes", id, len(content[id]))
		}
	}
	if cleared < 35000 {
		t.Errorf("cleared = %d tokens, want ≈40000", cleared)
	}
	if msgs[1+1].Result.Content != big {
		t.Error("Prune must not modify its input")
	}
}

func TestPruneIdempotent(t *testing.T) {
	var msgs []llm.Message
	msgs = append(msgs, llm.UserText("go"))
	for _, id := range []string{"a", "b", "c"} {
		msgs = append(msgs, toolTurn(id, strings.Repeat("z", 80000))...)
	}
	msgs = append(msgs, llm.UserText("2"), llm.UserText("3"))
	once, _ := ctxmgr.Prune(msgs, 10000)
	twice, cleared := ctxmgr.Prune(once, 10000)
	if cleared != 0 {
		t.Errorf("second prune cleared %d more tokens", cleared)
	}
	for i := range once {
		if once[i].Text() != twice[i].Text() || (once[i].Result != nil && once[i].Result.Content != twice[i].Result.Content) {
			t.Fatal("pruning twice must equal pruning once (stable requests)")
		}
	}
}

func TestEstimateMessages(t *testing.T) {
	var c ctxmgr.Calibrator
	msgs := append([]llm.Message{llm.UserText(strings.Repeat("a", 400))}, toolTurn("x", strings.Repeat("b", 800))...)
	got := c.EstimateRequest("sys", nil, msgs)
	if got < 300 || got > 330 {
		t.Errorf("estimate = %d, want ≈ (3+400+800+args)/4", got)
	}
}

func TestManagerPrunesNearTheLimit(t *testing.T) {
	m := ctxmgr.NewManager(ctxmgr.Budget{Window: 60000, Reserve: 10000}, 0.7, 5000)
	var msgs []llm.Message
	msgs = append(msgs, llm.UserText("go"))
	for _, id := range []string{"a", "b", "c", "d"} {
		msgs = append(msgs, toolTurn(id, strings.Repeat("q", 40000))...)
	}
	msgs = append(msgs, llm.UserText("2"), llm.UserText("3"))
	out, info := m.Transform("sys", nil, msgs)
	if info["pruned_tokens"].(int) == 0 || info["estimated"].(int) >= 40000 {
		t.Errorf("40k tokens > 70%% of 50k usable: should prune; info = %v", info)
	}
	if info["usable"] != 50000 {
		t.Errorf("info = %v", info)
	}
	if len(out) != len(msgs) {
		t.Error("message count changed")
	}
	small := []llm.Message{llm.UserText("hi")}
	if _, info := m.Transform("sys", nil, small); info["pruned_tokens"].(int) != 0 {
		t.Error("below the threshold nothing is pruned")
	}
}
