package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Gateway struct {
	baseURL string
	apiKey  string
	http    *http.Client
	retry   retryPolicy
	pricing map[string]Pricing
}

type Option func(*Gateway)

// WithRetry sets the number of retries and the backoff range.
func WithRetry(max int, initial, cap time.Duration) Option {
	return func(g *Gateway) { g.retry = retryPolicy{max: max, initial: initial, cap: cap} }
}

func WithHTTPClient(c *http.Client) Option { return func(g *Gateway) { g.http = c } }

// WithPricing supplies catalog prices used when the gateway omits usage.cost.
func WithPricing(p map[string]Pricing) Option { return func(g *Gateway) { g.pricing = p } }

func NewGateway(baseURL, apiKey string, opts ...Option) *Gateway {
	g := &Gateway{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: http.DefaultClient, retry: defaultRetry}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *Gateway) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		msg := g.run(ctx, req, ch)
		ch <- StreamEvent{Type: EventDone, Message: msg}
	}()
	return ch
}

func (g *Gateway) run(ctx context.Context, req Request, ch chan<- StreamEvent) *Message {
	fail := func(stop StopReason, format string, args ...any) *Message {
		return &Message{Role: RoleAssistant, Model: req.Model, Stop: stop, Err: fmt.Sprintf(format, args...)}
	}
	body, err := EncodeRequest(req)
	if err != nil {
		return fail(StopError, "encode request: %v", err)
	}
	resp, err := g.send(ctx, body)
	if err != nil {
		if ctx.Err() != nil {
			return fail(StopAborted, "aborted")
		}
		return fail(StopError, "%v", err)
	}
	defer resp.Body.Close()

	acc := newAccumulator(req.Model, ch)
	err = ReadSSE(resp.Body, acc.chunk)
	msg := acc.message()
	switch {
	case ctx.Err() != nil:
		msg.Stop, msg.Err = StopAborted, "aborted"
	case err != nil:
		msg.Stop, msg.Err = StopError, err.Error()
	case msg.Stop == "":
		msg.Stop, msg.Err = StopError, "stream ended without finish_reason"
	}
	if msg.Usage != nil && msg.Usage.CostUSD == 0 {
		if p, ok := g.pricing[req.Model]; ok {
			msg.Usage.CostUSD = CostFromUsage(*msg.Usage, p)
		}
	}
	return msg
}

// send posts the request, retrying transient failures. Retries happen only
// before any response body is read, so no partial output is ever duplicated.
func (g *Gateway) send(ctx context.Context, body []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Accept", "text/event-stream")
		if g.apiKey != "" {
			hreq.Header.Set("Authorization", "Bearer "+g.apiKey)
		}
		resp, err := g.http.Do(hreq)
		var header http.Header
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, err
			}
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		default:
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			err = &APIError{Status: resp.StatusCode, Message: errorMessage(b)}
			if !retryable(resp.StatusCode) {
				return nil, err
			}
			header = resp.Header
		}
		if attempt >= g.retry.max {
			return nil, err
		}
		select {
		case <-time.After(g.retry.backoff(attempt, header)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// APIError is a non-200 gateway response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("gateway: HTTP %d: %s", e.Status, e.Message) }

func errorMessage(body []byte) string {
	var e struct {
		Error wireError `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(string(body))
}

// accumulator folds stream chunks into a Message while forwarding deltas.
type accumulator struct {
	ch       chan<- StreamEvent
	model    string
	text     strings.Builder
	thinking strings.Builder
	details  map[int]map[string]any
	calls    map[int]*ToolCall
	args     map[int]*strings.Builder
	finish   string
	usage    *Usage
}

func newAccumulator(model string, ch chan<- StreamEvent) *accumulator {
	return &accumulator{ch: ch, model: model, details: map[int]map[string]any{},
		calls: map[int]*ToolCall{}, args: map[int]*strings.Builder{}}
}

func (a *accumulator) chunk(data []byte) error {
	var c wireChunk
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("bad stream chunk: %w", err)
	}
	if c.Error != nil {
		return errors.New("gateway stream error: " + c.Error.Message)
	}
	if c.Model != "" {
		a.model = c.Model
	}
	if c.Usage != nil {
		u := c.Usage
		a.usage = &Usage{
			Input: u.PromptTokens, Output: u.CompletionTokens,
			CacheRead: u.PromptDetails.CachedTokens, CacheWrite: u.CacheCreationTokens,
			Reasoning: u.CompletionDetails.ReasoningTokens, SurchargeUSD: u.SurchargeCost,
		}
		if u.Cost != nil {
			a.usage.CostUSD = *u.Cost
		}
	}
	for _, choice := range c.Choices {
		d := choice.Delta
		if d.Reasoning != "" {
			a.thinking.WriteString(d.Reasoning)
			a.ch <- StreamEvent{Type: EventThinkingDelta, Text: d.Reasoning}
		}
		for _, rd := range d.ReasoningDetails {
			a.mergeDetail(rd)
		}
		if d.Content != "" {
			a.text.WriteString(d.Content)
			a.ch <- StreamEvent{Type: EventTextDelta, Text: d.Content}
		}
		for _, tc := range d.ToolCalls {
			i := 0
			if tc.Index != nil {
				i = *tc.Index
			}
			call, ok := a.calls[i]
			if !ok {
				call = &ToolCall{}
				a.calls[i] = call
				a.args[i] = &strings.Builder{}
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Function.Name != "" {
				call.Name += tc.Function.Name
			}
			if !ok {
				a.ch <- StreamEvent{Type: EventToolCallStart, Index: i, Call: &ToolCall{ID: call.ID, Name: call.Name}}
			}
			if frag := tc.Function.Arguments; frag != "" {
				a.args[i].WriteString(frag)
				a.ch <- StreamEvent{Type: EventToolCallDelta, Index: i, Text: frag}
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			a.finish = *choice.FinishReason
		}
	}
	return nil
}

// mergeDetail combines reasoning_details fragments that share an index:
// string fields concatenate (text and signature arrive in separate fragments),
// other fields take the latest value.
func (a *accumulator) mergeDetail(rd map[string]any) {
	i := 0
	if f, ok := rd["index"].(float64); ok {
		i = int(f)
	}
	cur, ok := a.details[i]
	if !ok {
		a.details[i] = rd
		return
	}
	for k, v := range rd {
		if s, ok := v.(string); ok {
			if prev, ok := cur[k].(string); ok && k != "type" && k != "format" && k != "id" {
				cur[k] = prev + s
				continue
			}
		}
		cur[k] = v
	}
}

func (a *accumulator) message() *Message {
	m := &Message{Role: RoleAssistant, Model: a.model, Usage: a.usage}
	if a.thinking.Len() > 0 || len(a.details) > 0 {
		th := &Thinking{Text: a.thinking.String()}
		if len(a.details) > 0 {
			idx := make([]int, 0, len(a.details))
			for i := range a.details {
				idx = append(idx, i)
			}
			sort.Ints(idx)
			list := make([]map[string]any, len(idx))
			for j, i := range idx {
				list[j] = a.details[i]
			}
			th.Raw, _ = json.Marshal(list)
			if th.Text == "" {
				for _, d := range list {
					if s, ok := d["text"].(string); ok {
						th.Text += s
					}
				}
			}
		}
		m.Blocks = append(m.Blocks, Block{Thinking: th})
	}
	if a.text.Len() > 0 {
		m.Blocks = append(m.Blocks, Block{Text: a.text.String()})
	}
	idx := make([]int, 0, len(a.calls))
	for i := range a.calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		c := a.calls[i]
		c.Args = validArgs(a.args[i].String())
		m.Blocks = append(m.Blocks, Block{ToolCall: c})
	}
	switch a.finish {
	case "":
	case "length":
		m.Stop = StopLength
	case "tool_calls", "function_call":
		m.Stop = StopToolUse
	default:
		m.Stop = StopEnd
	}
	// Some providers report "stop" even when the message contains tool calls.
	if len(idx) > 0 && m.Stop == StopEnd {
		m.Stop = StopToolUse
	}
	return m
}

// validArgs keeps tool arguments valid JSON so messages always serialize
// (traces, sessions). No arguments become {}; malformed or truncated JSON is
// stored as a JSON string, which tool validation then reports to the model.
func validArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return json.RawMessage("{}")
	case json.Valid([]byte(s)):
		return json.RawMessage(s)
	}
	q, _ := json.Marshal(s)
	return q
}
