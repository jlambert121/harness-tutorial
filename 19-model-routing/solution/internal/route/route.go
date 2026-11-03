// Package route decides which model handles what: role resolution, history
// transformation when the model changes, per-run routers, and small-model
// background jobs such as session titles.
package route

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"

	"harness/internal/llm"
)

// Resolve returns the first non-empty of: flag, $envKey, config, def.
func Resolve(flag, envKey, config, def string) string {
	for _, v := range []string{flag, os.Getenv(envKey), config} {
		if v != "" {
			return v
		}
	}
	return def
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// TransformForModel prepares a history written by one or more models for
// target: thinking produced by another model is dropped (its signature would
// be rejected, and as text it reads like part of the answer), tool-call IDs
// are rewritten to match the strictest provider pattern (Anthropic:
// ^[a-zA-Z0-9_-]{1,64}$) consistently in calls and results, and aborted or
// failed assistant turns are dropped. pi's transform-messages.ts does the same.
func TransformForModel(msgs []llm.Message, target string) []llm.Message {
	ids := map[string]string{}
	fix := func(id string) string {
		if validID.MatchString(id) {
			return id
		}
		if v, ok := ids[id]; ok {
			return v
		}
		sum := sha1.Sum([]byte(id))
		v := "call_" + hex.EncodeToString(sum[:12])
		ids[id] = v
		return v
	}
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && (m.Stop == llm.StopAborted || m.Stop == llm.StopError) && len(m.ToolCalls()) == 0 {
			continue
		}
		m.Blocks = append([]llm.Block(nil), m.Blocks...)
		kept := m.Blocks[:0]
		for _, b := range m.Blocks {
			if b.Thinking != nil && m.Model != target {
				continue
			}
			if b.ToolCall != nil {
				c := *b.ToolCall
				c.ID = fix(c.ID)
				b.ToolCall = &c
			}
			kept = append(kept, b)
		}
		m.Blocks = kept
		if m.Result != nil {
			r := *m.Result
			r.CallID = fix(r.CallID)
			m.Result = &r
		}
		out = append(out, m)
	}
	return out
}

// Router picks the model for each step of a run.
type Router interface {
	Pick(step int, history []llm.Message) string
}

// Heuristic routes once per run from the run's first prompt: words that
// suggest hard work go to Main, everything else to Small.
type Heuristic struct {
	Small, Main string
	mu          sync.Mutex
	runStart    int
	choice      string
}

var hardWords = regexp.MustCompile(`(?i)\b(race|concurren\w*|deadlock|performance|slow|optimi[sz]e|architect\w*|design|refactor\w*|debug\w*|security|spec(ification)?|precedence|why)\b`)

func (h *Heuristic) Pick(step int, history []llm.Message) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if step > 1 && h.choice != "" {
		return h.choice
	}
	prompt := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleUser {
			prompt = history[i].Blocks[len(history[i].Blocks)-1].Text
			break
		}
	}
	h.choice = h.Small
	if hardWords.MatchString(prompt) || len(prompt) > 600 {
		h.choice = h.Main
	}
	return h.choice
}

// Escalating starts every session on Small and switches to Main for good
// after Streak consecutive failed tool results: cheap until the work proves
// hard (SWE-Router's finding: run cheap first, escalate on evidence).
// It never switches back; each switch costs a cold cache and drops thinking.
type Escalating struct {
	small, main string
	streak      int
	mu          sync.Mutex
	escalated   bool
}

func NewEscalating(small, main string, streak int) *Escalating {
	return &Escalating{small: small, main: main, streak: streak}
}

func (e *Escalating) Escalated() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.escalated }

func (e *Escalating) Pick(step int, history []llm.Message) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.escalated {
		return e.main
	}
	// Count consecutive failed tool results back to the last success or user message.
	fails := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role == llm.RoleUser {
			break
		}
		if m.Role != llm.RoleTool {
			continue
		}
		if !m.Result.IsError {
			break
		}
		fails++
	}
	if fails >= e.streak {
		e.escalated = true
		return e.main
	}
	return e.small
}

// Title asks the small model for a short session title.
func Title(ctx context.Context, c llm.Client, model, firstPrompt string) (string, error) {
	req := llm.Request{Model: model, MaxTokens: 30,
		System:   "Write a title of at most 6 words for a coding session that starts with the user's message. Reply with the title only, no quotes or punctuation at the end.",
		Messages: []llm.Message{llm.UserText(firstPrompt)}}
	var msg *llm.Message
	for ev := range c.Stream(ctx, req) {
		if ev.Type == llm.EventDone {
			msg = ev.Message
		}
	}
	if msg.Stop == llm.StopError {
		return "", errors.New(msg.Err)
	}
	t := strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg.Text()), "\n", 2)[0])
	t = strings.Trim(t, `"'.`)
	if len(t) > 60 {
		t = t[:60]
	}
	return t, nil
}
