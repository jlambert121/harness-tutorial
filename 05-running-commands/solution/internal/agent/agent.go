package agent

import (
	"context"
	"errors"
	"time"

	"harness/internal/llm"
	"harness/internal/tools"
)

type Config struct {
	Client    llm.Client
	Model     string
	System    string
	Tools     *tools.Registry
	MaxSteps  int // 0 = unlimited
	MaxTokens int
	Reasoning string
}

type Agent struct {
	cfg     Config
	history []llm.Message
	usage   llm.Usage
}

func New(cfg Config) *Agent { return &Agent{cfg: cfg} }

func (a *Agent) History() []llm.Message { return append([]llm.Message(nil), a.history...) }

func (a *Agent) Usage() llm.Usage { return a.usage }

func (a *Agent) Tools() *tools.Registry { return a.cfg.Tools }

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
	emit(Event{Kind: AgentStart, Text: input})
	defer func() { emit(Event{Kind: AgentEnd, Usage: &runUsage}) }()

	a.history = append(a.history, llm.UserText(input))
	for step := 1; ; step++ {
		final := a.cfg.MaxSteps > 0 && step == a.cfg.MaxSteps
		emit(Event{Kind: TurnStart, Turn: step})

		req := llm.Request{
			Model: a.cfg.Model, System: a.cfg.System, Messages: a.history,
			MaxTokens: a.cfg.MaxTokens, Reasoning: a.cfg.Reasoning,
		}
		if a.cfg.Tools != nil {
			req.Tools = a.cfg.Tools.Specs()
		}
		if final {
			req.Messages = append(append([]llm.Message(nil), a.history...), llm.UserText(MaxStepsPrompt))
			req.Extra = map[string]any{"tool_choice": "none"}
		}

		msg := a.sample(ctx, req, step, emit)
		if msg.Usage != nil {
			a.usage.Add(*msg.Usage)
			runUsage.Add(*msg.Usage)
		}
		switch msg.Stop {
		case llm.StopError:
			emit(Event{Kind: Error, Turn: step, Text: msg.Err})
			return errors.New(msg.Err)
		case llm.StopAborted:
			a.keepPartial(msg)
			return ctx.Err()
		}
		a.history = append(a.history, *msg)
		emit(Event{Kind: MessageEnd, Turn: step, Message: msg, Usage: msg.Usage})

		calls := msg.ToolCalls()
		if len(calls) == 0 {
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
			a.history = append(a.history, llm.Message{Role: llm.RoleTool, Result: &results[i]})
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
		a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Model: msg.Model,
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
