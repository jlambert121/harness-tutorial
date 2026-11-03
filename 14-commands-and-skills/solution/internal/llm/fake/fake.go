// Package fake is a scripted stand-in for the gateway's Chat Completions
// endpoint. Each request consumes the next Turn and receives it as an SSE
// stream shaped like the real gateway's (see research-notes.md, probe results).
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"harness/internal/llm"
)

type Turn struct {
	Text     string
	Thinking string
	Calls    []llm.ToolCall
	Usage    llm.Usage

	// Finish overrides finish_reason ("stop", "tool_calls", "length").
	Finish string
	// Status, Header and Body produce an HTTP error instead of a stream.
	Status int
	Header http.Header
	Body   string
	// ChunkSize splits text and tool arguments into pieces of this many bytes (default 4).
	ChunkSize int
	// Interleave alternates argument fragments of parallel tool calls.
	Interleave bool
	// Delay is slept between chunks; HangAfter stops sending after N chunks and waits for the client to go away.
	Delay     time.Duration
	HangAfter int
	// Respond computes the turn from the request body; it overrides the static fields.
	Respond func(req map[string]any) Turn
}

type Server struct {
	URL string

	mu       sync.Mutex
	turns    []Turn
	requests []json.RawMessage
	t        testing.TB
}

// NewServer starts a fake gateway and returns its base URL and a function
// returning every request body received so far.
func NewServer(t testing.TB, turns ...Turn) (baseURL string, requests func() []json.RawMessage) {
	s := Start(t, turns...)
	return s.URL, s.Requests
}

func Start(t testing.TB, turns ...Turn) *Server {
	s := &Server{turns: turns, t: t}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Push appends turns to the script.
func (s *Server) Push(turns ...Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = append(s.turns, turns...)
}

func (s *Server) Requests() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]json.RawMessage(nil), s.requests...)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, body)
	if len(s.turns) == 0 {
		s.mu.Unlock()
		s.t.Errorf("fake: request %d has no scripted turn", len(s.requests))
		http.Error(w, `{"error":{"message":"fake: no more scripted turns"}}`, http.StatusInternalServerError)
		return
	}
	turn := s.turns[0]
	s.turns = s.turns[1:]
	s.mu.Unlock()

	if turn.Respond != nil {
		var req map[string]any
		json.Unmarshal(body, &req)
		turn = turn.Respond(req)
	}
	if turn.Status != 0 && turn.Status != http.StatusOK {
		for k, v := range turn.Header {
			w.Header()[k] = v
		}
		b := turn.Body
		if b == "" {
			b = fmt.Sprintf(`{"error":{"message":"fake error %d"}}`, turn.Status)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(turn.Status)
		io.WriteString(w, b)
		return
	}

	var model string
	var req struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &req) == nil {
		model = req.Model
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	sent := 0
	send := func(chunk map[string]any) bool {
		if turn.HangAfter > 0 && sent >= turn.HangAfter {
			<-r.Context().Done()
			return false
		}
		chunk["id"] = "gen_fake"
		chunk["object"] = "chat.completion.chunk"
		chunk["model"] = model
		b, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		sent++
		if turn.Delay > 0 {
			select {
			case <-time.After(turn.Delay):
			case <-r.Context().Done():
				return false
			}
		}
		return true
	}
	for _, c := range Chunks(turn) {
		if !send(c) {
			return
		}
	}
	io.WriteString(w, "data: [DONE]\n\n")
}

// Chunks renders a turn as Chat Completions stream chunks (without the SSE framing).
func Chunks(turn Turn) []map[string]any {
	size := turn.ChunkSize
	if size <= 0 {
		size = 4
	}
	delta := func(d map[string]any) map[string]any {
		return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": nil}}}
	}
	out := []map[string]any{delta(map[string]any{"role": "assistant"})}

	if turn.Thinking != "" {
		for _, piece := range split(turn.Thinking, size) {
			out = append(out, delta(map[string]any{
				"reasoning": piece,
				"reasoning_details": []any{map[string]any{
					"type": "reasoning.text", "text": piece, "format": "anthropic-claude-v1", "index": 0,
				}},
			}))
		}
		out = append(out, delta(map[string]any{
			"reasoning": "",
			"reasoning_details": []any{map[string]any{
				"type": "reasoning.text", "text": "", "signature": "sig_" + fmt.Sprint(len(turn.Thinking)),
				"format": "anthropic-claude-v1", "index": 0,
			}},
		}))
	}
	for _, piece := range split(turn.Text, size) {
		out = append(out, delta(map[string]any{"content": piece}))
	}

	type frag struct {
		index int
		args  string
	}
	var starts, frags []map[string]any
	var pending [][]frag
	for i, c := range turn.Calls {
		starts = append(starts, delta(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": c.ID, "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": ""},
		}}}))
		var fs []frag
		for _, piece := range split(string(c.Args), size) {
			fs = append(fs, frag{i, piece})
		}
		pending = append(pending, fs)
	}
	argChunk := func(f frag) map[string]any {
		return delta(map[string]any{"tool_calls": []any{map[string]any{
			"index": f.index, "function": map[string]any{"arguments": f.args},
		}}})
	}
	if turn.Interleave {
		out = append(out, starts...)
		for more := true; more; {
			more = false
			for i := range pending {
				if len(pending[i]) > 0 {
					frags = append(frags, argChunk(pending[i][0]))
					pending[i] = pending[i][1:]
					more = true
				}
			}
		}
		out = append(out, frags...)
	} else {
		for i := range starts {
			out = append(out, starts[i])
			for _, f := range pending[i] {
				out = append(out, argChunk(f))
			}
		}
	}

	finish := turn.Finish
	if finish == "" {
		finish = "stop"
		if len(turn.Calls) > 0 {
			finish = "tool_calls"
		}
	}
	u := turn.Usage
	if u.Input == 0 && u.Output == 0 {
		u.Input = 10 + len(turn.Text)/4
		u.Output = 1 + (len(turn.Text)+len(turn.Thinking))/4
	}
	usage := map[string]any{
		"prompt_tokens": u.Input, "completion_tokens": u.Output, "total_tokens": u.Input + u.Output,
		"prompt_tokens_details":       map[string]any{"cached_tokens": u.CacheRead},
		"completion_tokens_details":   map[string]any{"reasoning_tokens": u.Reasoning},
		"cache_creation_input_tokens": u.CacheWrite,
	}
	if u.CostUSD != 0 {
		usage["cost"] = u.CostUSD
	}
	if u.SurchargeUSD != 0 {
		usage["surcharge_cost"] = u.SurchargeUSD
	}
	out = append(out, map[string]any{
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage":   usage,
	})
	return out
}

func split(s string, n int) []string {
	var out []string
	for len(s) > n {
		cut := n
		for cut < len(s) && !utf8Start(s[cut]) {
			cut++
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Call builds a tool call for tests.
func Call(id, name string, args any) llm.ToolCall {
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Args: b}
}

// Text returns a Turn that answers with plain text.
func Text(s string) Turn { return Turn{Text: s} }

// Tools returns a Turn that calls the given tools.
func Tools(calls ...llm.ToolCall) Turn { return Turn{Calls: calls} }

// LastUserText extracts the final user message text from a request body, for Respond funcs.
func LastUserText(req map[string]any) string {
	msgs, _ := req["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		m, _ := msgs[i].(map[string]any)
		if m["role"] == "user" {
			switch c := m["content"].(type) {
			case string:
				return c
			case []any:
				var sb strings.Builder
				for _, p := range c {
					if pm, ok := p.(map[string]any); ok {
						if t, ok := pm["text"].(string); ok {
							sb.WriteString(t)
						}
					}
				}
				return sb.String()
			}
		}
	}
	return ""
}
