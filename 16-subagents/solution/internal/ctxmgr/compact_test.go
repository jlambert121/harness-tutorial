package ctxmgr_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/ctxmgr"
	"harness/internal/llm"
	"harness/internal/llm/fake"
)

func conversation(n int, size int) []llm.Message {
	var msgs []llm.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs, llm.UserText("task "+string(rune('A'+i))))
		msgs = append(msgs, toolTurn("c"+string(rune('a'+i)), strings.Repeat("x", size))...)
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Text: "done " + string(rune('A'+i))}}})
	}
	return msgs
}

func TestSplitPointNeverOrphans(t *testing.T) {
	msgs := conversation(6, 8000) // ≈2k tokens per turn
	for _, keep := range []int{0, 500, 2500, 5000, 9000, 1 << 30} {
		cut := ctxmgr.SplitPoint(msgs, keep)
		if cut < 0 || cut > len(msgs) {
			t.Fatalf("keep=%d: cut %d out of range", keep, cut)
		}
		if cut > 0 && msgs[cut].Role == llm.RoleTool {
			t.Errorf("keep=%d: cut at %d is a tool result; its call would be summarized away", keep, cut)
		}
	}
	if cut := ctxmgr.SplitPoint(msgs, 5000); cut == 0 || len(msgs)-cut < 4 {
		t.Errorf("keep=5000 tokens should keep about two turns, cut=%d of %d", cut, len(msgs))
	}
	if cut := ctxmgr.SplitPoint(msgs, 1<<30); cut != 0 {
		t.Errorf("keeping everything means nothing to summarize, cut=%d", cut)
	}
}

func TestSplitPointInsideOneLongTurn(t *testing.T) {
	msgs := []llm.Message{llm.UserText("one big task")}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		msgs = append(msgs, toolTurn(id, strings.Repeat("x", 8000))...)
	}
	cut := ctxmgr.SplitPoint(msgs, 3000)
	if cut == 0 || msgs[cut].Role != llm.RoleAssistant {
		t.Errorf("a single-prompt run must still be compactable, cutting before an assistant message; cut=%d", cut)
	}
}

func TestSerialize(t *testing.T) {
	msgs := []llm.Message{
		llm.UserText("fix the bug"),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Text: "Looking."}, {ToolCall: &llm.ToolCall{ID: "1", Name: "read", Args: json.RawMessage(`{"path":"a.go"}`)}}}},
		{Role: llm.RoleTool, Result: &llm.ToolResult{CallID: "1", Name: "read", Content: strings.Repeat("z", 5000)}},
	}
	s := ctxmgr.Serialize(msgs)
	for _, want := range []string{"[User]: fix the bug", "[Assistant]: Looking.", `[Assistant tool call]: read({"path":"a.go"})`, "[Tool result]: zzz", "chars truncated"} {
		if !strings.Contains(s, want) {
			t.Errorf("Serialize missing %q:\n%s", want, s)
		}
	}
	if len(s) > 3000 {
		t.Errorf("tool results must be cut to about 2000 chars; got %d", len(s))
	}
}

func TestCompactSummarizesPrefixWithParentRequest(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("## Goal\nfinish task F"))
	c := ctxmgr.NewCompactor(llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), "m", 5000)
	msgs := conversation(6, 8000)
	tools := []llm.ToolSpec{{Name: "read", Schema: json.RawMessage(`{"type":"object"}`)}}
	res, err := c.Compact(context.Background(), "sys", tools, msgs, ctxmgr.CompactOptions{Context: "<environment>cwd</environment>"})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages   []map[string]any `json:"messages"`
		Tools      []any            `json:"tools"`
		ToolChoice string           `json:"tool_choice"`
	}
	json.Unmarshal(reqs()[0], &body)
	if contentText(body.Messages[0]) != "sys" || len(body.Tools) != 1 || body.ToolChoice != "none" {
		t.Errorf("summarize request must reuse the session's system prompt and tools (cache hit) with tool_choice none")
	}
	last := body.Messages[len(body.Messages)-1]
	if last["role"] != "user" || !strings.Contains(contentText(last), "## Next steps") {
		t.Errorf("last message should be the compaction prompt: %v", last)
	}
	if n := len(body.Messages) - 2; n != res.Cut {
		t.Errorf("the summarized prefix (%d messages) should be sent as-is; Cut=%d", n, res.Cut)
	}
	if res.Summary != "## Goal\nfinish task F" {
		t.Errorf("summary = %q", res.Summary)
	}
	h := res.History
	first := h[0]
	if first.Role != llm.RoleUser || !strings.Contains(first.Text(), "<conversation-summary>") || !strings.Contains(first.Text(), "finish task F") || !strings.Contains(first.Text(), "<environment>cwd</environment>") {
		t.Errorf("first message must carry the summary and re-inject the context: %q", first.Text())
	}
	if len(h)-1 != len(msgs)-res.Cut || h[len(h)-1].Text() != msgs[len(msgs)-1].Text() {
		t.Errorf("the kept tail must follow the summary unchanged")
	}
}

// contentText reads a wire message's content whether it is a string or text parts.
func contentText(m map[string]any) string {
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, p := range c {
			b.WriteString(p.(map[string]any)["text"].(string))
		}
		return b.String()
	}
	return ""
}

func TestCompactReinjectsRecentFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // current content"), 0o644)
	os.WriteFile(filepath.Join(dir, "huge.go"), []byte(strings.Repeat("h", 100000)), 0o644)
	url, _ := fake.NewServer(t, fake.Text("summary"))
	c := ctxmgr.NewCompactor(llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), "m", 5000)
	res, _ := c.Compact(context.Background(), "sys", nil, conversation(6, 8000), ctxmgr.CompactOptions{
		RecentFiles: []string{filepath.Join(dir, "main.go"), filepath.Join(dir, "huge.go"), filepath.Join(dir, "gone.go")},
	})
	text := res.History[0].Text()
	if !strings.Contains(text, "package main // current content") {
		t.Error("recently used files should be re-attached with their current content")
	}
	if strings.Contains(text, strings.Repeat("h", 30000)) {
		t.Error("files over 5k tokens are referenced by path only")
	}
	if !strings.Contains(text, "huge.go") {
		t.Error("large files should still be named")
	}
}

func TestIterativeSummaryUsesPrior(t *testing.T) {
	url, reqs := fake.NewServer(t, fake.Text("first summary"), fake.Text("second summary"))
	c := ctxmgr.NewCompactor(llm.NewGateway(url, "k", llm.WithRetry(0, 0, 0)), "m", 3000)
	res, _ := c.Compact(context.Background(), "sys", nil, conversation(6, 8000), ctxmgr.CompactOptions{})
	grown := append(res.History, conversation(4, 8000)...)
	res2, err := c.Compact(context.Background(), "sys", nil, grown, ctxmgr.CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(reqs()[1])
	if !strings.Contains(body, "prior-summary") || !strings.Contains(body, "first summary") || !strings.Contains(body, "Anything you do not carry") {
		t.Errorf("a second compaction must update the earlier summary, not start over: %s", body[:min(len(body), 600)])
	}
	if !strings.Contains(res2.History[0].Text(), "second summary") || strings.Contains(res2.History[0].Text(), "first summary") {
		t.Errorf("the new summary replaces the old one: %q", res2.History[0].Text())
	}
}

func TestCompactNothingToDo(t *testing.T) {
	c := ctxmgr.NewCompactor(nil, "m", 1<<30)
	if _, err := c.Compact(context.Background(), "sys", nil, conversation(2, 100), ctxmgr.CompactOptions{}); err != ctxmgr.ErrNothingToCompact {
		t.Errorf("err = %v", err)
	}
}

func TestIsOverflow(t *testing.T) {
	for _, s := range []string{
		"gateway: HTTP 400: prompt is too long: 599026 tokens > 200000 maximum",
		"gateway: HTTP 400: This model's maximum context length is 128000 tokens",
		"gateway: HTTP 400: input exceeds the context window",
	} {
		if !ctxmgr.IsOverflow(s) {
			t.Errorf("IsOverflow(%q) = false", s)
		}
	}
	if ctxmgr.IsOverflow("gateway: HTTP 402: insufficient credits") {
		t.Error("402 is not an overflow")
	}
}
