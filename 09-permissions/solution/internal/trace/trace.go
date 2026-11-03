// Package trace records agent events as JSONL and renders them as timelines.
package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"harness/internal/agent"
)

// NewRecorder writes each event as one JSON line.
func NewRecorder(w io.Writer) agent.Sink {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return func(e agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(e)
	}
}

func ReadEvents(r io.Reader) ([]agent.Event, error) {
	var out []agent.Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e agent.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return out, fmt.Errorf("trace line %d: %w", len(out)+1, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Span is one bar on the timeline: a model call or a tool execution.
type Span struct {
	Kind       string // "model" or "tool"
	Turn       int
	Label      string
	Start, End time.Duration // offsets from the first event
	FirstToken time.Duration // model spans: when streaming began (0 if never)
	Detail     string
	IsError    bool
}

func Spans(events []agent.Event) []Span {
	if len(events) == 0 {
		return nil
	}
	t0 := events[0].Time
	off := func(t time.Time) time.Duration { return t.Sub(t0) }
	var spans []Span
	var cur *Span
	tools := map[string]int{}
	for _, e := range events {
		switch e.Kind {
		case agent.TurnStart:
			spans = append(spans, Span{Kind: "model", Turn: e.Turn, Label: fmt.Sprintf("turn %d model", e.Turn), Start: off(e.Time)})
			cur = &spans[len(spans)-1]
		case agent.TextDelta, agent.ThinkingDelta, agent.ToolCallStart:
			if cur != nil && cur.FirstToken == 0 {
				cur.FirstToken = off(e.Time)
			}
		case agent.MessageEnd, agent.Error:
			if cur != nil {
				cur.End = off(e.Time)
				if e.Usage != nil {
					cur.Detail = fmt.Sprintf("in %d (cached %d) · out %d · $%.5f", e.Usage.Input, e.Usage.CacheRead, e.Usage.Output, e.Usage.CostUSD)
				}
				if e.Kind == agent.Error {
					cur.IsError, cur.Detail = true, e.Text
				}
				cur = nil
			}
		case agent.ToolStart:
			args := ""
			if e.Call != nil {
				args = compact(string(e.Call.Args), 60)
				tools[e.Call.ID] = len(spans)
			}
			spans = append(spans, Span{Kind: "tool", Turn: e.Turn, Label: e.Call.Name + " " + args, Start: off(e.Time)})
		case agent.ToolEnd:
			if e.Call == nil {
				continue
			}
			if i, ok := tools[e.Call.ID]; ok {
				spans[i].End = off(e.Time)
				if e.Result != nil {
					spans[i].IsError = e.Result.IsError
					spans[i].Detail = fmt.Sprintf("%d bytes: %s", len(e.Result.Content), compact(e.Result.Content, 80))
				}
			}
		}
	}
	end := off(events[len(events)-1].Time)
	for i := range spans {
		if spans[i].End == 0 {
			spans[i].End = end
		}
	}
	return spans
}

func compact(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

// RenderASCII draws one row per span. Model rows show waiting (░) then streaming (█).
func RenderASCII(events []agent.Event, w io.Writer, width int) {
	spans := Spans(events)
	if len(spans) == 0 {
		return
	}
	total := spans[len(spans)-1].End
	for _, s := range spans {
		total = max(total, s.End)
	}
	if total <= 0 {
		total = 1
	}
	col := func(d time.Duration) int { return int(float64(d) / float64(total) * float64(width-1)) }
	for _, s := range spans {
		row := []rune(strings.Repeat(" ", width))
		a, b := col(s.Start), max(col(s.End), col(s.Start))
		ft := b + 1
		if s.Kind == "model" && s.FirstToken > 0 {
			ft = col(s.FirstToken)
		}
		for i := a; i <= b && i < width; i++ {
			switch {
			case s.Kind == "tool":
				row[i] = '▓'
			case i < ft:
				row[i] = '░'
			default:
				row[i] = '█'
			}
		}
		mark := " "
		if s.IsError {
			mark = "!"
		}
		fmt.Fprintf(w, "%-34s|%s| %7s %s %s\n", compact(s.Label, 34), string(row), (s.End - s.Start).Round(time.Millisecond), mark, s.Detail)
	}
	fmt.Fprintf(w, "%-34s total %s   ░ waiting for first token  █ streaming  ▓ tool\n", "", total.Round(time.Millisecond))
}
