package tui_test

import (
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/ui/tui"
)

func TestStatusShowsCacheHitRate(t *testing.T) {
	m := tui.FromEvents([]agent.Event{
		{Kind: agent.TurnStart, Turn: 1, Time: time.Now()},
		{Kind: agent.MessageEnd, Turn: 1, Usage: &llm.Usage{Input: 10000, CacheRead: 9000}, Time: time.Now()},
	}, 140, 24)
	if !strings.Contains(m.Status(), "cache 90%") {
		t.Errorf("status %q should show the last request's cache hit rate", m.Status())
	}
}
