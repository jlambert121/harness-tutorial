package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type StopReason string

const (
	StopEnd     StopReason = "stop"
	StopLength  StopReason = "length"
	StopToolUse StopReason = "tool_use"
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted"
)

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// Thinking holds model reasoning. Raw is the gateway's reasoning_details array,
// replayed verbatim on later requests; providers verify signatures inside it.
type Thinking struct {
	Text string          `json:"text,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

// Block is one piece of message content. Exactly one field is set.
type Block struct {
	Text     string    `json:"text,omitempty"`
	Thinking *Thinking `json:"thinking,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
}

type Message struct {
	Role   Role        `json:"role"`
	Blocks []Block     `json:"blocks,omitempty"`
	Result *ToolResult `json:"result,omitempty"` // set iff Role == RoleTool

	// Assistant-only metadata.
	Model string     `json:"model,omitempty"`
	Usage *Usage     `json:"usage,omitempty"`
	Stop  StopReason `json:"stop,omitempty"`
	Err   string     `json:"err,omitempty"`
}

func UserText(s string) Message { return Message{Role: RoleUser, Blocks: []Block{{Text: s}}} }

// Text concatenates the message's text blocks.
func (m Message) Text() string {
	var s string
	for _, b := range m.Blocks {
		s += b.Text
	}
	return s
}

func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, b := range m.Blocks {
		if b.ToolCall != nil {
			calls = append(calls, *b.ToolCall)
		}
	}
	return calls
}

type Usage struct {
	Input      int     `json:"input"` // total prompt tokens, including CacheRead and CacheWrite
	Output     int     `json:"output"`
	CacheRead  int     `json:"cache_read,omitempty"`
	CacheWrite int     `json:"cache_write,omitempty"`
	Reasoning  int     `json:"reasoning,omitempty"`
	CostUSD    float64 `json:"cost_usd"`
	// SurchargeUSD is per-request gateway fees (e.g. zero data retention) on top of CostUSD.
	SurchargeUSD float64 `json:"surcharge_usd,omitempty"`
}

func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Reasoning += o.Reasoning
	u.CostUSD += o.CostUSD
	u.SurchargeUSD += o.SurchargeUSD
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
	// Reasoning is "" (provider default), "enabled", or an effort level:
	// "none", "minimal", "low", "medium", "high", "xhigh", "max".
	Reasoning string
	// Extra is merged into the top level of the request body (e.g. "providerOptions").
	Extra map[string]any
	// Cache selects prompt-cache markers; SessionID pins the gateway's
	// session affinity so requests reach the provider holding the cache.
	Cache     CacheMode
	SessionID string
	// Fallbacks are tried by the gateway, in order, if Model fails.
	Fallbacks []string
}

type CacheMode string

const (
	CacheOff    CacheMode = "off"    // no markers (providers with implicit caching still cache)
	CacheAuto   CacheMode = "auto"   // providerOptions.gateway.caching = "auto"
	CacheManual CacheMode = "manual" // cache_control on the system prompt and the last two user/tool messages
)

type StreamEventType string

const (
	EventTextDelta     StreamEventType = "text_delta"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventToolCallStart StreamEventType = "toolcall_start"
	EventToolCallDelta StreamEventType = "toolcall_delta"
	EventDone          StreamEventType = "done"
)

type StreamEvent struct {
	Type    StreamEventType
	Text    string    // delta text for text, thinking, and tool-call argument deltas
	Index   int       // tool-call index for toolcall_* events
	Call    *ToolCall // toolcall_start: ID and Name
	Message *Message  // done: the complete assistant message
}

// Client streams one model response. Stream never fails directly: the channel
// always ends with exactly one EventDone whose Message.Stop is "error" or
// "aborted" when something went wrong.
type Client interface {
	Stream(ctx context.Context, req Request) <-chan StreamEvent
}
