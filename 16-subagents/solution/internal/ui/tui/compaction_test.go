package tui_test

import (
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/ui/tui"
)

func TestCompactionShown(t *testing.T) {
	m := tui.FromEvents([]agent.Event{
		{Kind: agent.Compaction, Data: map[string]any{"reason": "threshold", "before": 42, "after": 7}, Time: time.Now()},
	}, 100, 20)
	if tr := m.Transcript(); !strings.Contains(tr, "compacted") || !strings.Contains(tr, "42") || !strings.Contains(tr, "7") {
		t.Errorf("transcript = %q", tr)
	}
}
