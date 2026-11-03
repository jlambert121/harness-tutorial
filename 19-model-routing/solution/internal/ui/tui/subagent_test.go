package tui_test

import (
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/ui/tui"
)

func TestSubagentCell(t *testing.T) {
	ev := func(phase string, extra map[string]any) agent.Event {
		d := map[string]any{"id": "s1", "agent": "explore", "description": "find the loop", "phase": phase}
		for k, v := range extra {
			d[k] = v
		}
		return agent.Event{Kind: agent.Subagent, Data: d, Time: time.Now()}
	}
	m := tui.FromEvents([]agent.Event{
		ev("start", map[string]any{"model": "small"}),
		ev("tool", map[string]any{"tool": "grep"}),
		ev("tool", map[string]any{"tool": "read"}),
		ev("end", map[string]any{"cost_usd": 0.0021}),
	}, 100, 20)
	tr := m.Transcript()
	if strings.Count(tr, "explore") != 1 || !strings.Contains(tr, "find the loop") || !strings.Contains(tr, "2 tool calls") || !strings.Contains(tr, "$0.0021") {
		t.Errorf("one cell per subagent, updated in place:\n%s", tr)
	}
}
