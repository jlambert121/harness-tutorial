package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func collect(t *testing.T, ch <-chan llm.StreamEvent) ([]llm.StreamEvent, *llm.Message) {
	t.Helper()
	var events []llm.StreamEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if len(events) == 0 || events[len(events)-1].Type != llm.EventDone {
					t.Fatal("stream closed without a done event")
				}
				return events, events[len(events)-1].Message
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

func fastClient(url string) llm.Client {
	return llm.NewGateway(url, "test-key", llm.WithRetry(3, 10*time.Millisecond, 50*time.Millisecond))
}

func TestStreamText(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Turn{Text: "hello world", ChunkSize: 3,
		Usage: llm.Usage{Input: 12, Output: 3, CostUSD: 0.0001, SurchargeUSD: 0.0002}})
	events, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{
		Model: "m", Messages: []llm.Message{llm.UserText("hi")},
	}))

	var text string
	var deltas int
	for _, ev := range events {
		if ev.Type == llm.EventTextDelta {
			text += ev.Text
			deltas++
		}
	}
	if text != "hello world" || deltas < 3 {
		t.Errorf("deltas joined = %q over %d events; want streamed pieces of %q", text, deltas, "hello world")
	}
	if msg.Role != llm.RoleAssistant || msg.Text() != "hello world" || msg.Stop != llm.StopEnd || msg.Model != "m" {
		t.Errorf("final message = %+v", msg)
	}
	if msg.Usage == nil || msg.Usage.Input != 12 || msg.Usage.Output != 3 || msg.Usage.CostUSD != 0.0001 || msg.Usage.SurchargeUSD != 0.0002 {
		t.Errorf("usage = %+v", msg.Usage)
	}
	if n := len(reqs()); n != 1 {
		t.Errorf("requests = %d", n)
	}
}

func TestStreamSendsAuthHeader(t *testing.T) {
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer ts.Close()
	_, msg := collect(t, llm.NewGateway(ts.URL, "secret").Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if auth != "Bearer secret" || msg.Text() != "ok" {
		t.Errorf("auth=%q text=%q", auth, msg.Text())
	}
}

func TestStreamInterleavedToolCalls(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{
		Interleave: true, ChunkSize: 3,
		Calls: []llm.ToolCall{
			fake.Call("call_a", "read", map[string]string{"path": "a.go"}),
			fake.Call("call_b", "read", map[string]string{"path": "b.go"}),
		},
	})
	events, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))

	starts := 0
	for _, ev := range events {
		if ev.Type == llm.EventToolCallStart {
			starts++
			if ev.Call == nil || ev.Call.ID == "" || ev.Call.Name != "read" {
				t.Errorf("toolcall_start without id/name: %+v", ev)
			}
		}
	}
	if starts != 2 {
		t.Errorf("toolcall_start events = %d, want 2", starts)
	}
	calls := msg.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call_a" || string(calls[0].Args) != `{"path":"a.go"}` ||
		calls[1].ID != "call_b" || string(calls[1].Args) != `{"path":"b.go"}` {
		t.Errorf("calls reassembled wrong: %s %s / %s %s", calls[0].ID, calls[0].Args, calls[1].ID, calls[1].Args)
	}
	if msg.Stop != llm.StopToolUse {
		t.Errorf("stop = %q", msg.Stop)
	}
}

func TestStopWithToolCallsBecomesToolUse(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Finish: "stop", Calls: []llm.ToolCall{fake.Call("c1", "ls", map[string]string{})}})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopToolUse {
		t.Errorf("a response with tool calls must report tool_use even if finish_reason is stop; got %q", msg.Stop)
	}
}

func TestStreamLengthStop(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Text: "cut off", Finish: "length"})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopLength {
		t.Errorf("stop = %q", msg.Stop)
	}
}

func TestStreamThinkingMergesFragments(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Thinking: "consider the file", Text: "done", ChunkSize: 5})
	events, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	var thought string
	for _, ev := range events {
		if ev.Type == llm.EventThinkingDelta {
			thought += ev.Text
		}
	}
	if thought != "consider the file" {
		t.Errorf("thinking deltas = %q", thought)
	}
	if len(msg.Blocks) < 2 || msg.Blocks[0].Thinking == nil {
		t.Fatalf("first block should be thinking: %+v", msg.Blocks)
	}
	th := msg.Blocks[0].Thinking
	if th.Text != "consider the file" {
		t.Errorf("thinking text = %q", th.Text)
	}
	var details []map[string]any
	if err := json.Unmarshal(th.Raw, &details); err != nil {
		t.Fatalf("Raw is not a JSON array: %s", th.Raw)
	}
	if len(details) != 1 || details[0]["text"] != "consider the file" || details[0]["signature"] != "sig_17" || details[0]["format"] != "anthropic-claude-v1" {
		t.Errorf("fragments not merged by index into one detail: %s", th.Raw)
	}
}

func TestUsageCacheFields(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Text: "ok", Usage: llm.Usage{Input: 8416, Output: 4, CacheRead: 8412, Reasoning: 2, CostUSD: 0.00009}})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	u := msg.Usage
	if u.Input != 8416 || u.CacheRead != 8412 || u.Reasoning != 2 {
		t.Errorf("usage = %+v", u)
	}
}

func TestCostFallbackFromPricing(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Text: "ok", Usage: llm.Usage{Input: 1000, Output: 100}})
	c := llm.NewGateway(url, "k", llm.WithPricing(map[string]llm.Pricing{"m": {Input: 0.000002, Output: 0.00001}}))
	_, msg := collect(t, c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if got, want := msg.Usage.CostUSD, 0.003; diff(got, want) > 1e-12 {
		t.Errorf("CostUSD = %v, want %v", got, want)
	}
}

func TestRetryHonorsRetryAfter(t *testing.T) {
	url, reqs := fake.NewServer(t,
		fake.Turn{Status: 429, Header: http.Header{"Retry-After": {"1"}}},
		fake.Text("ok"),
	)
	start := time.Now()
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopEnd || msg.Text() != "ok" {
		t.Fatalf("msg = %+v", msg)
	}
	if el := time.Since(start); el < time.Second {
		t.Errorf("retried after %v; Retry-After asked for 1s", el)
	}
	if n := len(reqs()); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestRetryOn5xxThenGivesUp(t *testing.T) {
	url, reqs := fake.NewServer(t,
		fake.Turn{Status: 503}, fake.Turn{Status: 502}, fake.Turn{Status: 500}, fake.Turn{Status: 500},
	)
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopError || !strings.Contains(msg.Err, "500") {
		t.Errorf("msg = %+v", msg)
	}
	if n := len(reqs()); n != 4 {
		t.Errorf("requests = %d, want 4 (1 + 3 retries)", n)
	}
}

func TestNoRetryOn402(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Turn{Status: 402, Body: `{"error":{"message":"insufficient credits"}}`})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopError || !strings.Contains(msg.Err, "402") || !strings.Contains(msg.Err, "insufficient credits") {
		t.Errorf("msg = %+v", msg)
	}
	if n := len(reqs()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestContextOverflowSurfacedNotRetried(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Turn{Status: 400,
		Body: `{"error":{"message":"prompt is too long: 599026 tokens > 200000 maximum","type":"AI_APICallError"}}`})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	if msg.Stop != llm.StopError || !strings.Contains(msg.Err, "prompt is too long") || len(reqs()) != 1 {
		t.Errorf("msg = %+v, requests = %d", msg, len(reqs()))
	}
}

func TestStreamCancelKeepsPartial(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Text: "partial answer that never finishes", ChunkSize: 4, HangAfter: 3})
	ctx, cancel := context.WithCancel(context.Background())
	ch := fastClient(url).Stream(ctx, llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}})
	msgCh := make(chan *llm.Message, 1)
	go func() {
		for ev := range ch {
			if ev.Type == llm.EventTextDelta {
				cancel()
			}
			if ev.Type == llm.EventDone {
				msgCh <- ev.Message
			}
		}
	}()
	select {
	case msg := <-msgCh:
		if msg.Stop != llm.StopAborted {
			t.Errorf("stop = %q, want aborted", msg.Stop)
		}
		if msg.Text() == "" {
			t.Errorf("partial text should be kept on abort")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the stream")
	}
}

func TestCostFromUsage(t *testing.T) {
	p := llm.Pricing{Input: 0.000002, Output: 0.00001, CacheRead: 0.0000002, CacheWrite: 0.0000025}
	u := llm.Usage{Input: 10000, Output: 500, CacheRead: 6000, CacheWrite: 3000}
	// 1000 uncached*2e-6 + 6000*2e-7 + 3000*2.5e-6 + 500*1e-5
	want := 0.002 + 0.0012 + 0.0075 + 0.005
	if got := llm.CostFromUsage(u, p); diff(got, want) > 1e-12 {
		t.Errorf("CostFromUsage = %v, want %v", got, want)
	}
	// A model without cache-write pricing bills cache writes at the input rate.
	p.CacheWrite = 0
	want = 0.002 + 0.0012 + 0.006 + 0.005
	if got := llm.CostFromUsage(u, p); diff(got, want) > 1e-12 {
		t.Errorf("CostFromUsage without CacheWrite price = %v, want %v", got, want)
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestToolArgsAlwaysValidJSON(t *testing.T) {
	url, _ := fake.NewServer(t, fake.Turn{Calls: []llm.ToolCall{
		{ID: "c1", Name: "todo_list", Args: nil},
		{ID: "c2", Name: "read", Args: json.RawMessage(`{"path": "a.go"`)},
	}})
	_, msg := collect(t, fastClient(url).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("x")}}))
	calls := msg.ToolCalls()
	if string(calls[0].Args) != "{}" {
		t.Errorf("a call with no arguments must get {} (got %q); empty RawMessage breaks json.Marshal of the whole message", calls[0].Args)
	}
	if !json.Valid(calls[1].Args) {
		t.Errorf("truncated arguments must be stored as valid JSON (got %q)", calls[1].Args)
	}
	if _, err := json.Marshal(msg); err != nil {
		t.Errorf("the final message must always be serializable (traces, sessions): %v", err)
	}
}
