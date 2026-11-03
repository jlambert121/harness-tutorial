package tui_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/ui/tui"
)

func sampleEvents() []agent.Event {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	call := &llm.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"x.go"}`)}
	usage := &llm.Usage{Input: 1200, Output: 40, CostUSD: 0.0012}
	return []agent.Event{
		{Kind: agent.AgentStart, Text: "what is in x.go?", Time: at(0)},
		{Kind: agent.TurnStart, Turn: 1, Time: at(1)},
		{Kind: agent.TextDelta, Turn: 1, Text: "Let me ", Time: at(500)},
		{Kind: agent.TextDelta, Turn: 1, Text: "look.", Time: at(520)},
		{Kind: agent.ToolCallStart, Turn: 1, Call: &llm.ToolCall{ID: "c1", Name: "read"}, Time: at(530)},
		{Kind: agent.MessageEnd, Turn: 1, Usage: usage, Time: at(600)},
		{Kind: agent.ToolStart, Turn: 1, Call: call, Time: at(601)},
		{Kind: agent.ToolEnd, Turn: 1, Call: call, Result: &llm.ToolResult{CallID: "c1", Name: "read", Content: "     1\tpackage x\n     2\tfunc X() {}\n"}, Time: at(605)},
		{Kind: agent.TurnEnd, Turn: 1, Time: at(606)},
		{Kind: agent.TurnStart, Turn: 2, Time: at(607)},
		{Kind: agent.TextDelta, Turn: 2, Text: "It defines X.", Time: at(900)},
		{Kind: agent.MessageEnd, Turn: 2, Usage: &llm.Usage{Input: 1300, Output: 10, CostUSD: 0.0011}, Time: at(950)},
		{Kind: agent.TurnEnd, Turn: 2, Time: at(951)},
		{Kind: agent.AgentEnd, Usage: &llm.Usage{Input: 2500, Output: 50, CostUSD: 0.0023}, Time: at(952)},
	}
}

func TestTranscriptRendersEvents(t *testing.T) {
	m := tui.FromEvents(sampleEvents(), 80, 24)
	tr := m.Transcript()
	for _, want := range []string{"what is in x.go?", "Let me look.", "read", "x.go", "package x", "It defines X."} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing %q:\n%s", want, tr)
		}
	}
	if strings.Index(tr, "Let me look.") > strings.Index(tr, "package x") || strings.Index(tr, "package x") > strings.Index(tr, "It defines X.") {
		t.Errorf("cells out of order:\n%s", tr)
	}
}

func TestStatusBar(t *testing.T) {
	m := tui.FromEvents(sampleEvents(), 100, 24)
	status := m.Status()
	for _, want := range []string{"1300", "$0.0023"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q missing %q (context = last request's input tokens; cost = session total)", status, want)
		}
	}
}

func TestReplayDeterministic(t *testing.T) {
	a := tui.FromEvents(sampleEvents(), 80, 24)
	b := tui.FromEvents(sampleEvents(), 80, 24)
	if a.View().Content != b.View().Content {
		t.Error("the same events must render the same screen")
	}
}

func TestWindowResizeRewraps(t *testing.T) {
	m := tui.FromEvents(sampleEvents(), 80, 24)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if w := lineWidth(line); w > 30 {
			t.Fatalf("line wider than terminal (%d > 30): %q", w, line)
		}
	}
}

func TestToolErrorMarked(t *testing.T) {
	ev := sampleEvents()
	ev[7].Result.IsError = true
	ev[7].Result.Content = "cannot read x.go: no such file"
	tr := tui.FromEvents(ev, 80, 24).Transcript()
	if !strings.Contains(tr, "✗") || !strings.Contains(tr, "no such file") {
		t.Errorf("error result not marked:\n%s", tr)
	}
}

func lineWidth(s string) int { return lipgloss.Width(s) }
