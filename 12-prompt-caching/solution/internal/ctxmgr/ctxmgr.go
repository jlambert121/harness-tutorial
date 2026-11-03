// Package ctxmgr keeps requests inside the model's context window:
// budgeting, token estimation calibrated against provider usage, and pruning.
package ctxmgr

import (
	"sync"

	"harness/internal/llm"
)

// Budget is the context window minus room reserved for the response.
type Budget struct {
	Window, Reserve int
}

func (b Budget) Usable() int { return b.Window - b.Reserve }

// NewBudget reads the window from the catalog unless window > 0 overrides it.
// The reserve is min(20000, max output tokens), opencode's rule.
func NewBudget(info llm.ModelInfo, window int) Budget {
	b := Budget{Window: info.ContextWindow, Reserve: 20000}
	if info.MaxTokens > 0 && info.MaxTokens < b.Reserve {
		b.Reserve = info.MaxTokens
	}
	if window > 0 {
		b.Window = window
		b.Reserve = min(b.Reserve, window/4)
	}
	return b
}

// Calibrator turns the bytes/4 rule of thumb into an estimate that tracks
// what the provider actually counts, using an exponentially weighted ratio.
type Calibrator struct {
	mu    sync.Mutex
	ratio float64
}

func (c *Calibrator) Ratio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ratio == 0 {
		return 1
	}
	return c.ratio
}

// Observe records a request's estimated tokens against the provider's count.
func (c *Calibrator) Observe(estimated, actual int) {
	if estimated <= 0 || actual <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r := float64(actual) / float64(estimated)
	if c.ratio == 0 {
		c.ratio = r
	} else {
		c.ratio = 0.7*c.ratio + 0.3*r
	}
}

func (c *Calibrator) Estimate(s string) int { return c.scale((len(s) + 3) / 4) }

func (c *Calibrator) scale(n int) int { return int(float64(n)*c.Ratio() + 0.5) }

// EstimateRequest estimates the input tokens of a request.
func (c *Calibrator) EstimateRequest(system string, tools []llm.ToolSpec, msgs []llm.Message) int {
	n := len(system)
	for _, t := range tools {
		n += len(t.Name) + len(t.Description) + len(t.Schema)
	}
	for _, m := range msgs {
		n += messageBytes(m)
	}
	return c.scale((n + 3) / 4)
}

func messageBytes(m llm.Message) int {
	n := 0
	for _, b := range m.Blocks {
		n += len(b.Text)
		if b.ToolCall != nil {
			n += len(b.ToolCall.Name) + len(b.ToolCall.Args)
		}
		if b.Thinking != nil {
			n += len(b.Thinking.Raw) + len(b.Thinking.Text)
		}
	}
	if m.Result != nil {
		n += len(m.Result.Content)
	}
	return n
}

// ClearedMarker replaces pruned tool output (opencode's wording).
const ClearedMarker = "[Old tool result content cleared]"

// Prune clears the content of older tool results, newest first kept: tool
// output in the last two user turns is never touched, then protect tokens of
// older output are kept, and everything earlier is replaced by ClearedMarker.
// Messages are never removed, so tool calls keep their results. It returns a
// copy and the number of tokens cleared.
func Prune(msgs []llm.Message, protect int) ([]llm.Message, int) {
	out := append([]llm.Message(nil), msgs...)
	users, kept, cleared := 0, 0, 0
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		if m.Role == llm.RoleUser {
			users++
			continue
		}
		if users < 2 || m.Result == nil || m.Result.Content == ClearedMarker {
			continue
		}
		tokens := (len(m.Result.Content) + 3) / 4
		if kept+tokens <= protect {
			kept += tokens
			continue
		}
		r := *m.Result
		cleared += tokens
		r.Content = ClearedMarker
		m.Result = &r
		out[i] = m
	}
	return out, cleared
}

// Boundary returns the index just past the last tool result Prune would
// clear, or 0 if it would clear nothing.
func Boundary(msgs []llm.Message, protect int) int {
	out, _ := Prune(msgs, protect)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Result != nil && out[i].Result.Content == ClearedMarker &&
			(msgs[i].Result == nil || msgs[i].Result.Content != ClearedMarker) {
			return i + 1
		}
	}
	return 0
}

// ClearBefore clears every tool result before index idx. It copies.
func ClearBefore(msgs []llm.Message, idx int) ([]llm.Message, int) {
	out := append([]llm.Message(nil), msgs...)
	cleared := 0
	for i := 0; i < idx && i < len(out); i++ {
		if r := out[i].Result; r != nil && r.Content != ClearedMarker {
			cleared += (len(r.Content) + 3) / 4
			c := *r
			c.Content = ClearedMarker
			out[i].Result = &c
		}
	}
	return out, cleared
}

// Manager applies the budget before every request.
//
// Pruning changes earlier messages, which invalidates the provider's cached
// prefix from that point on. So the manager keeps a boundary: tool results
// before it are cleared, after it are kept. The boundary only moves when at
// least MinClear tokens can be cleared beyond it; between moves, requests
// are byte-identical extensions of each other and keep hitting the cache.
type Manager struct {
	Budget    Budget
	Threshold float64 // prune when the estimate exceeds this fraction of Usable
	Protect   int     // tokens of older tool output to keep when pruning
	MinClear  int     // minimum tokens a boundary move must clear (opencode: 20000)
	Cal       Calibrator
	last      int
	boundary  int
}

func NewManager(b Budget, threshold float64, protect int) *Manager {
	return &Manager{Budget: b, Threshold: threshold, Protect: protect}
}

// Reset forgets the pruning boundary (call after the history is replaced, e.g. by compaction).
func (m *Manager) Reset() { m.boundary = 0 }

// Transform is an agent.Config.Transform: it returns the messages to send and
// numbers for the UI (estimated, usable, pruned_tokens, ratio).
func (m *Manager) Transform(system string, tools []llm.ToolSpec, msgs []llm.Message) ([]llm.Message, map[string]any) {
	est := m.Cal.EstimateRequest(system, tools, msgs)
	pruned := 0
	if m.boundary > len(msgs) {
		m.boundary = 0
	}
	if float64(est) > m.Threshold*float64(m.Budget.Usable()) {
		if b := Boundary(msgs, m.Protect); b > m.boundary {
			_, gain := ClearBefore(msgs[m.boundary:b], b-m.boundary)
			if gain >= m.MinClear {
				m.boundary = b
				pruned = m.Cal.scale(gain)
			}
		}
	}
	if m.boundary > 0 {
		msgs, _ = ClearBefore(msgs, m.boundary)
		est = m.Cal.EstimateRequest(system, tools, msgs)
	}
	m.last = est
	return msgs, map[string]any{"estimated": est, "usable": m.Budget.Usable(), "window": m.Budget.Window,
		"pruned_tokens": pruned, "ratio": m.Cal.Ratio()}
}

// Observe feeds the provider's input count for the last transformed request.
func (m *Manager) Observe(u llm.Usage) { m.Cal.Observe(m.last, u.Input) }
