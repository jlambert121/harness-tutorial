package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/tools"
)

type harness struct {
	agent  *agent.Agent
	events []agent.Event
	reqs   func() []json.RawMessage
	root   string
}

func newHarness(t *testing.T, maxSteps int, turns ...fake.Turn) *harness {
	t.Helper()
	url, reqs := fake.NewServer(t, turns...)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "x.txt"), []byte("contents of x"), 0o644)
	reg := tools.NewRegistry(&tools.Env{Root: root})
	reg.Register(tools.ReadFile())
	reg.Register(slowTool())
	h := &harness{reqs: reqs, root: root}
	h.agent = agent.New(agent.Config{
		Client: llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)),
		Model:  "m", System: "sys", Tools: reg, MaxSteps: maxSteps,
	})
	return h
}

func (h *harness) run(ctx context.Context, input string) error {
	return h.agent.Run(ctx, input, func(e agent.Event) { h.events = append(h.events, e) })
}

func (h *harness) kinds() []agent.EventKind {
	var ks []agent.EventKind
	for _, e := range h.events {
		if e.Kind != agent.TextDelta && e.Kind != agent.ThinkingDelta {
			ks = append(ks, e.Kind)
		}
	}
	return ks
}

// slowTool blocks until its context is cancelled.
func slowTool() tools.Tool {
	return tools.Func{
		Def: llm.ToolSpec{Name: "slow", Description: "never finishes", Schema: json.RawMessage(`{"type":"object"}`)},
		Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
			<-ctx.Done()
			return tools.Result{Content: "interrupted", IsError: true}
		},
	}
}

func request(t *testing.T, raw json.RawMessage) (msgs []map[string]any, body map[string]any) {
	t.Helper()
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, m := range body["messages"].([]any) {
		msgs = append(msgs, m.(map[string]any))
	}
	return msgs, body
}

func TestLoopRunsToolsUntilStop(t *testing.T) {
	h := newHarness(t, 0,
		fake.Tools(fake.Call("c1", "read_file", map[string]string{"path": "x.txt"})),
		fake.Text("x says: contents of x"),
	)
	if err := h.run(context.Background(), "what is in x.txt?"); err != nil {
		t.Fatal(err)
	}
	hist := h.agent.History()
	if len(hist) != 4 {
		t.Fatalf("history has %d messages, want user, assistant(call), tool, assistant", len(hist))
	}
	if hist[0].Role != llm.RoleUser || hist[1].Role != llm.RoleAssistant || hist[2].Role != llm.RoleTool || hist[3].Role != llm.RoleAssistant {
		t.Errorf("roles = %s %s %s %s", hist[0].Role, hist[1].Role, hist[2].Role, hist[3].Role)
	}
	if r := hist[2].Result; r == nil || r.CallID != "c1" || r.Content != "contents of x" {
		t.Errorf("tool result = %+v", hist[2].Result)
	}
	if hist[3].Text() != "x says: contents of x" {
		t.Errorf("final = %q", hist[3].Text())
	}

	reqs := h.reqs()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	msgs, body := request(t, reqs[1])
	if len(msgs) != 4 || msgs[0]["role"] != "system" || msgs[3]["role"] != "tool" || msgs[3]["tool_call_id"] != "c1" {
		t.Errorf("second request must replay the whole conversation; got %v", msgs)
	}
	if body["tools"] == nil {
		t.Error("tools missing from request")
	}
}

func TestEventOrder(t *testing.T) {
	h := newHarness(t, 0,
		fake.Tools(fake.Call("c1", "read_file", map[string]string{"path": "x.txt"})),
		fake.Text("done"),
	)
	h.run(context.Background(), "go")
	want := []agent.EventKind{
		agent.AgentStart,
		agent.TurnStart, agent.ToolCallStart, agent.MessageEnd, agent.ToolStart, agent.ToolEnd, agent.TurnEnd,
		agent.TurnStart, agent.MessageEnd, agent.TurnEnd,
		agent.AgentEnd,
	}
	got := h.kinds()
	if len(got) != len(want) {
		t.Fatalf("events = %v\nwant     %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v\nwant     %v", got, want)
		}
	}
	last := h.events[len(h.events)-1]
	if last.Usage == nil || last.Usage.Output == 0 {
		t.Errorf("agent_end should carry total usage: %+v", last.Usage)
	}
	for _, e := range h.events {
		if e.Time.IsZero() {
			t.Fatalf("event %s has no timestamp", e.Kind)
		}
	}
}

func TestHistoryCarriesAcrossRuns(t *testing.T) {
	h := newHarness(t, 0, fake.Text("first answer"), fake.Text("second answer"))
	h.run(context.Background(), "one")
	h.run(context.Background(), "two")
	msgs, _ := request(t, h.reqs()[1])
	if len(msgs) != 4 || msgs[1]["content"] != "one" || msgs[2]["content"] != "first answer" || msgs[3]["content"] != "two" {
		t.Errorf("second run request = %v", msgs)
	}
}

func TestMaxStepsFinalTurnDisablesTools(t *testing.T) {
	loop := fake.Tools(fake.Call("c", "read_file", map[string]string{"path": "x.txt"}))
	h := newHarness(t, 2, loop, fake.Text("summary of progress"))
	if err := h.run(context.Background(), "loop forever"); err != nil {
		t.Fatal(err)
	}
	reqs := h.reqs()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	msgs, body := request(t, reqs[1])
	if body["tool_choice"] != "none" {
		t.Errorf("final step must send tool_choice none; got %v", body["tool_choice"])
	}
	last := msgs[len(msgs)-1]
	if last["role"] != "user" || !strings.Contains(last["content"].(string), "MAXIMUM STEPS REACHED") {
		t.Errorf("final step must tell the model it is out of steps; last message = %v", last)
	}
	for _, m := range h.agent.History() {
		if strings.Contains(m.Text(), "MAXIMUM STEPS REACHED") {
			t.Error("the step-limit notice is request-only and must not be stored in history")
		}
	}
}

func TestMaxStepsIgnoresLateToolCalls(t *testing.T) {
	loop := fake.Tools(fake.Call("c", "read_file", map[string]string{"path": "x.txt"}))
	stubborn := fake.Tools(fake.Call("c2", "read_file", map[string]string{"path": "x.txt"}))
	h := newHarness(t, 2, loop, stubborn)
	h.run(context.Background(), "go")
	hist := h.agent.History()
	last := hist[len(hist)-1]
	if last.Role != llm.RoleTool || !last.Result.IsError || !strings.Contains(last.Result.Content, "step limit") {
		t.Errorf("tool calls on the final step must get not-executed results; last = %+v", last)
	}
}

func TestLengthStopFailsToolCalls(t *testing.T) {
	h := newHarness(t, 0,
		fake.Turn{Finish: "length", Calls: []llm.ToolCall{fake.Call("c1", "read_file", map[string]string{"path": "x.txt"})}},
		fake.Text("ok"),
	)
	h.run(context.Background(), "go")
	hist := h.agent.History()
	r := hist[2].Result
	if r == nil || !r.IsError || !strings.Contains(r.Content, "truncated") {
		t.Errorf("a tool call cut off by max_tokens must not run; result = %+v", r)
	}
	for _, e := range h.events {
		if e.Kind == agent.ToolStart {
			t.Error("tool was executed despite length stop")
		}
	}
}

func TestUnknownToolDoesNotStopLoop(t *testing.T) {
	h := newHarness(t, 0, fake.Tools(fake.Call("c1", "nope", map[string]string{})), fake.Text("recovered"))
	if err := h.run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	hist := h.agent.History()
	if !hist[2].Result.IsError || hist[3].Text() != "recovered" {
		t.Errorf("history = %+v", hist)
	}
}

func TestCancelLeavesValidHistory(t *testing.T) {
	h := newHarness(t, 0, fake.Tools(
		fake.Call("c1", "read_file", map[string]string{"path": "x.txt"}),
		fake.Call("c2", "slow", map[string]string{}),
		fake.Call("c3", "read_file", map[string]string{"path": "x.txt"}),
	))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	err := h.run(ctx, "go")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run error = %v, want context.Canceled", err)
	}
	hist := h.agent.History()
	results := map[string]*llm.ToolResult{}
	for _, m := range hist {
		if m.Result != nil {
			results[m.Result.CallID] = m.Result
		}
	}
	for _, m := range hist {
		for _, c := range m.ToolCalls() {
			if results[c.ID] == nil {
				t.Errorf("tool call %s has no result; the next request would be rejected", c.ID)
			}
		}
	}
	if r := results["c3"]; r == nil || !strings.Contains(r.Content, "aborted") {
		t.Errorf("calls after the cancel point must be marked aborted; got %+v", r)
	}
}

func TestCancelDuringStreamDropsPartialToolCall(t *testing.T) {
	h := newHarness(t, 0, fake.Turn{
		Text:      "Let me look",
		Calls:     []llm.ToolCall{fake.Call("c1", "read_file", map[string]string{"path": "a-very-long-file-name.txt"})},
		ChunkSize: 3, HangAfter: 6,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	h.run(ctx, "go")
	for _, m := range h.agent.History() {
		if len(m.ToolCalls()) > 0 {
			t.Errorf("an aborted stream's half-written tool call must not be kept: %+v", m)
		}
	}
}

func TestModelErrorEndsRun(t *testing.T) {
	h := newHarness(t, 0, fake.Turn{Status: 400, Body: `{"error":{"message":"prompt is too long"}}`})
	err := h.run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "prompt is too long") {
		t.Errorf("err = %v", err)
	}
	var sawError bool
	for _, e := range h.events {
		if e.Kind == agent.Error && strings.Contains(e.Text, "prompt is too long") {
			sawError = true
		}
	}
	if !sawError {
		t.Error("no error event emitted")
	}
	if hist := h.agent.History(); len(hist) != 1 {
		t.Errorf("failed turn should leave only the user message, got %d messages", len(hist))
	}
}
