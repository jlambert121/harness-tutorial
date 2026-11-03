package ctxmgr_test

import (
	"fmt"
	"strings"
	"testing"

	"harness/internal/ctxmgr"
	"harness/internal/llm"
)

func TestClearBefore(t *testing.T) {
	msgs := append([]llm.Message{llm.UserText("go")}, toolTurn("a", "xxxx")...)
	msgs = append(msgs, toolTurn("b", "yyyy")...)
	out, cleared := ctxmgr.ClearBefore(msgs, 3)
	if out[2].Result.Content != ctxmgr.ClearedMarker || out[4].Result.Content != "yyyy" || cleared != 1 {
		t.Errorf("out=%v cleared=%d", out, cleared)
	}
	if msgs[2].Result.Content != "xxxx" {
		t.Error("input modified")
	}
}

// Each step appends a ~10k-token tool result. The manager may only move its
// pruning boundary when at least MinClear tokens can be cleared beyond it;
// between moves, every request must start with exactly the previous
// request's messages so the provider's prefix cache keeps hitting.
func TestManagerKeepsPrefixStableBetweenPrunes(t *testing.T) {
	m := ctxmgr.NewManager(ctxmgr.Budget{Window: 100000, Reserve: 10000}, 0.5, 20000)
	m.MinClear = 30000
	big := strings.Repeat("r", 40000)
	msgs := []llm.Message{llm.UserText("start")}
	var prev []llm.Message
	advances := 0
	for i := 0; i < 20; i++ {
		msgs = append(msgs, toolTurn(fmt.Sprint("c", i), big)...)
		tail := append(append([]llm.Message(nil), msgs...), llm.UserText("q2"), llm.UserText("q3"))
		out, info := m.Transform("sys", nil, tail)
		if info["pruned_tokens"].(int) > 0 {
			advances++
			if info["pruned_tokens"].(int) < 30000 {
				t.Errorf("step %d: boundary moved to clear only %d tokens (< MinClear)", i, info["pruned_tokens"])
			}
		} else if prev != nil {
			for j := 0; j < len(prev)-2; j++ {
				a, b := prev[j], out[j]
				if (a.Result == nil) != (b.Result == nil) || (a.Result != nil && a.Result.Content != b.Result.Content) {
					t.Fatalf("step %d: message %d changed without a boundary move; the cached prefix is lost", i, j)
				}
			}
		}
		prev = out
	}
	if advances < 2 || advances > 8 {
		t.Errorf("boundary advanced %d times over 20 steps; want a few large jumps, not one per step", advances)
	}
}
