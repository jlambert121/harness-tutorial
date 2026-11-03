package agent

import (
	"time"

	"harness/internal/llm"
)

type EventKind string

const (
	AgentStart    EventKind = "agent_start"
	TurnStart     EventKind = "turn_start"
	TextDelta     EventKind = "text_delta"
	ThinkingDelta EventKind = "thinking_delta"
	ToolCallStart EventKind = "toolcall_start" // the model began writing a tool call
	MessageEnd    EventKind = "message_end"    // assistant message complete
	ToolStart     EventKind = "tool_start"     // the harness began executing a tool
	ToolEnd       EventKind = "tool_end"
	TurnEnd       EventKind = "turn_end"
	AgentEnd      EventKind = "agent_end"
	Error         EventKind = "error"
	Steer         EventKind = "steer"      // a queued user message was added to the conversation
	ContextInfo   EventKind = "context"    // Data: what Config.Transform reported for this request
	Compaction    EventKind = "compaction" // Data: reason, before, after (message counts)
)

// Event is everything a UI, logger, or test needs to observe a run.
type Event struct {
	Kind    EventKind       `json:"kind"`
	Turn    int             `json:"turn,omitempty"`
	Text    string          `json:"text,omitempty"`
	Call    *llm.ToolCall   `json:"call,omitempty"`
	Result  *llm.ToolResult `json:"result,omitempty"`
	Message *llm.Message    `json:"message,omitempty"`
	Usage   *llm.Usage      `json:"usage,omitempty"`
	Data    map[string]any  `json:"data,omitempty"`
	Time    time.Time       `json:"time"`
}

type Sink func(Event)

// Tee fans one event stream out to several sinks; nil sinks are skipped.
func Tee(sinks ...Sink) Sink {
	return func(e Event) {
		for _, s := range sinks {
			if s != nil {
				s(e)
			}
		}
	}
}
