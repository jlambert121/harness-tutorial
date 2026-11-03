package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"harness/internal/llm"
	"harness/internal/tools"
)

type Config struct {
	Client    llm.Client
	Model     string
	System    string
	Context   string // prepended to the first user message (environment, project instructions)
	Tools     *tools.Registry
	MaxSteps  int // 0 = unlimited
	MaxTokens int
	Reasoning string
	Cache     llm.CacheMode
	SessionID string

	// Persist, if set, receives every message as it is appended to history.
	Persist func(llm.Message)
	// OnRunStart, if set, is called at the start of each Run (e.g. to snapshot files).
	OnRunStart func(ctx context.Context, input string)
	// Transform, if set, rewrites the messages sent on each request (history is
	// unchanged) and returns information for a ContextInfo event.
	Transform func(system string, tools []llm.ToolSpec, msgs []llm.Message) ([]llm.Message, map[string]any)

	// Compaction: ShouldCompact is asked before each request; Compact returns
	// the replacement history; IsOverflow recognizes a context-overflow error,
	// which triggers one compaction and a retry; OnCompact persists the result.
	ShouldCompact func(system string, tools []llm.ToolSpec, msgs []llm.Message) bool
	Compact       func(ctx context.Context, system string, tools []llm.ToolSpec, msgs []llm.Message, focus string) ([]llm.Message, error)
	IsOverflow    func(err string) bool
	OnCompact     func(history []llm.Message)
}

type Agent struct {
	cfg     Config
	history []llm.Message
	usage   llm.Usage

	mu        sync.Mutex
	steers    []string
	reminders []string
}

// Remind queues text for the next user message, wrapped in <system-reminder>.
// This is how state changes (mode switches, new context) reach the model
// without editing the system prompt, which would invalidate the cached prefix.
func (a *Agent) Remind(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reminders = append(a.reminders, text)
}

func (a *Agent) takeReminders() []llm.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []llm.Block
	for _, r := range a.reminders {
		out = append(out, llm.Block{Text: "<system-reminder>\n" + r + "\n</system-reminder>"})
	}
	a.reminders = nil
	return out
}

// Steer queues a user message to be added at the next safe point: after the
// current tool batch, or, if the model has just answered, as a new turn.
// It is safe to call from another goroutine while Run is active.
func (a *Agent) Steer(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steers = append(a.steers, msg)
}

// injectSteers appends queued messages to history and reports whether there were any.
func (a *Agent) injectSteers(step int, emit func(Event)) bool {
	a.mu.Lock()
	q := a.steers
	a.steers = nil
	a.mu.Unlock()
	for _, m := range q {
		a.appendHistory(llm.UserText(m))
		emit(Event{Kind: Steer, Turn: step, Text: m})
	}
	return len(q) > 0
}

func New(cfg Config) *Agent { return &Agent{cfg: cfg} }

func (a *Agent) History() []llm.Message { return append([]llm.Message(nil), a.history...) }

// Load replaces the conversation, e.g. when resuming a saved session.
func (a *Agent) Load(history []llm.Message) { a.history = append([]llm.Message(nil), history...) }

func (a *Agent) appendHistory(m llm.Message) {
	a.history = append(a.history, m)
	if a.cfg.Persist != nil {
		a.cfg.Persist(m)
	}
}

func (a *Agent) Usage() llm.Usage { return a.usage }

func (a *Agent) Tools() *tools.Registry { return a.cfg.Tools }

func (a *Agent) System() string { return a.cfg.System }

// MaxStepsPrompt is sent (not stored) on the last allowed step, adapted from opencode.
const MaxStepsPrompt = "CRITICAL - MAXIMUM STEPS REACHED. Tools are disabled until the next user input. " +
	"Respond with text only: summarize what you did, what remains, and what the user should do next."

const lengthStopResult = "Tool call not executed: the response hit the output token limit, so its arguments may be truncated. " +
	"Retry with a smaller call."

const stepLimitResult = "Tool call not executed: the step limit was reached."

// Run appends input to the conversation and loops until the model answers
// without tool calls, the step limit is hit, ctx is cancelled, or the model errors.
func (a *Agent) Run(ctx context.Context, input string, sink Sink) error {
	if sink == nil {
		sink = func(Event) {}
	}
	emit := func(e Event) {
		e.Time = time.Now()
		sink(e)
	}
	var runUsage llm.Usage
	if a.cfg.OnRunStart != nil {
		a.cfg.OnRunStart(ctx, input)
	}
	emit(Event{Kind: AgentStart, Text: input})
	defer func() { emit(Event{Kind: AgentEnd, Usage: &runUsage}) }()

	user := llm.UserText(input)
	user.Blocks = append(a.takeReminders(), user.Blocks...)
	if len(a.history) == 0 && a.cfg.Context != "" {
		user.Blocks = append([]llm.Block{{Text: a.cfg.Context}}, user.Blocks...)
	}
	a.appendHistory(user)
	overflowRetried := false
	for step := 1; ; step++ {
		final := a.cfg.MaxSteps > 0 && step == a.cfg.MaxSteps
		emit(Event{Kind: TurnStart, Turn: step})
		if a.cfg.ShouldCompact != nil && a.cfg.Compact != nil && a.cfg.ShouldCompact(a.cfg.System, a.specs(), a.history) {
			a.compact(ctx, "threshold", "", step, emit)
		}

		req := llm.Request{
			Model: a.cfg.Model, System: a.cfg.System, Messages: a.history,
			MaxTokens: a.cfg.MaxTokens, Reasoning: a.cfg.Reasoning,
			Cache: a.cfg.Cache, SessionID: a.cfg.SessionID,
		}
		if a.cfg.Tools != nil {
			req.Tools = a.cfg.Tools.Specs()
		}
		if a.cfg.Transform != nil {
			var info map[string]any
			req.Messages, info = a.cfg.Transform(req.System, req.Tools, req.Messages)
			emit(Event{Kind: ContextInfo, Turn: step, Data: info})
		}
		if final {
			req.Messages = append(append([]llm.Message(nil), req.Messages...), llm.UserText(MaxStepsPrompt))
			req.Extra = map[string]any{"tool_choice": "none"}
		}

		msg := a.sample(ctx, req, step, emit)
		if msg.Usage != nil {
			a.usage.Add(*msg.Usage)
			runUsage.Add(*msg.Usage)
		}
		switch msg.Stop {
		case llm.StopError:
			if !overflowRetried && a.cfg.IsOverflow != nil && a.cfg.Compact != nil && a.cfg.IsOverflow(msg.Err) {
				overflowRetried = true
				if a.compact(ctx, "overflow", "", step, emit) {
					emit(Event{Kind: TurnEnd, Turn: step})
					continue
				}
			}
			emit(Event{Kind: Error, Turn: step, Text: msg.Err})
			return errors.New(msg.Err)
		case llm.StopAborted:
			a.keepPartial(msg)
			return ctx.Err()
		}
		a.appendHistory(*msg)
		emit(Event{Kind: MessageEnd, Turn: step, Message: msg, Usage: msg.Usage})

		calls := msg.ToolCalls()
		if len(calls) == 0 {
			if !final && a.injectSteers(step, emit) {
				emit(Event{Kind: TurnEnd, Turn: step})
				continue
			}
			emit(Event{Kind: TurnEnd, Turn: step})
			return nil
		}

		var results []llm.ToolResult
		switch {
		case msg.Stop == llm.StopLength:
			results = notExecuted(calls, lengthStopResult)
		case final:
			results = notExecuted(calls, stepLimitResult)
		default:
			results = a.cfg.Tools.Execute(ctx, calls,
				func(c llm.ToolCall) { emit(Event{Kind: ToolStart, Turn: step, Call: &c}) },
				func(c llm.ToolCall, r llm.ToolResult) { emit(Event{Kind: ToolEnd, Turn: step, Call: &c, Result: &r}) })
		}
		for i := range results {
			a.appendHistory(llm.Message{Role: llm.RoleTool, Result: &results[i]})
		}
		if ctx.Err() == nil {
			a.injectSteers(step, emit)
		}
		emit(Event{Kind: TurnEnd, Turn: step})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if final {
			return nil
		}
	}
}

// Compact replaces the history with a summary now (the /compact command).
func (a *Agent) Compact(ctx context.Context, focus string, sink Sink) error {
	if a.cfg.Compact == nil {
		return errors.New("compaction is not configured")
	}
	if sink == nil {
		sink = func(Event) {}
	}
	if !a.compact(ctx, "manual", focus, 0, func(e Event) { e.Time = time.Now(); sink(e) }) {
		return errors.New("compaction failed")
	}
	return nil
}

func (a *Agent) specs() []llm.ToolSpec {
	if a.cfg.Tools == nil {
		return nil
	}
	return a.cfg.Tools.Specs()
}

func (a *Agent) compact(ctx context.Context, reason, focus string, step int, emit func(Event)) bool {
	before := len(a.history)
	h, err := a.cfg.Compact(ctx, a.cfg.System, a.specs(), a.history, focus)
	if err != nil {
		emit(Event{Kind: Error, Turn: step, Text: "compaction: " + err.Error()})
		return false
	}
	a.history = h
	if a.cfg.OnCompact != nil {
		a.cfg.OnCompact(h)
	}
	emit(Event{Kind: Compaction, Turn: step, Data: map[string]any{"reason": reason, "before": before, "after": len(h)}})
	return true
}

func (a *Agent) sample(ctx context.Context, req llm.Request, step int, emit func(Event)) *llm.Message {
	var msg *llm.Message
	for ev := range a.cfg.Client.Stream(ctx, req) {
		switch ev.Type {
		case llm.EventTextDelta:
			emit(Event{Kind: TextDelta, Turn: step, Text: ev.Text})
		case llm.EventThinkingDelta:
			emit(Event{Kind: ThinkingDelta, Turn: step, Text: ev.Text})
		case llm.EventToolCallStart:
			emit(Event{Kind: ToolCallStart, Turn: step, Call: ev.Call})
		case llm.EventDone:
			msg = ev.Message
		}
	}
	return msg
}

// keepPartial stores the visible text of an aborted response. Partial tool
// calls (possibly truncated JSON, no result) and partial thinking (possibly an
// incomplete signature) would make the next request invalid, so they are dropped.
func (a *Agent) keepPartial(msg *llm.Message) {
	if text := msg.Text(); text != "" {
		a.appendHistory(llm.Message{Role: llm.RoleAssistant, Model: msg.Model,
			Blocks: []llm.Block{{Text: text}}, Stop: llm.StopAborted})
	}
}

func notExecuted(calls []llm.ToolCall, why string) []llm.ToolResult {
	out := make([]llm.ToolResult, len(calls))
	for i, c := range calls {
		out[i] = llm.ToolResult{CallID: c.ID, Name: c.Name, Content: why, IsError: true}
	}
	return out
}
