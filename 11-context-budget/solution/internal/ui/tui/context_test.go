package tui_test

import (
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/ui/tui"
)

func TestContextMeter(t *testing.T) {
	now := time.Now()
	m := tui.FromEvents([]agent.Event{
		{Kind: agent.TurnStart, Turn: 1, Time: now},
		{Kind: agent.ContextInfo, Turn: 1, Data: map[string]any{"estimated": 8800, "usable": 180000, "pruned_tokens": 0}, Time: now},
		{Kind: agent.MessageEnd, Turn: 1, Usage: &llm.Usage{Input: 9000}, Time: now},
	}, 120, 24)
	if s := m.Status(); !strings.Contains(s, "9.0k/180k") || !strings.Contains(s, "5%") {
		t.Errorf("status %q should show context used/usable and a percentage", s)
	}
	m.Update(tui.EventMsg{Event: agent.Event{Kind: agent.ContextInfo, Turn: 2, Data: map[string]any{"estimated": 120000, "usable": 180000, "pruned_tokens": 30000}, Time: now}})
	if !strings.Contains(m.Transcript(), "pruned") || !strings.Contains(m.Transcript(), "30.0k") {
		t.Errorf("pruning should be visible in the transcript:\n%s", m.Transcript())
	}
}
