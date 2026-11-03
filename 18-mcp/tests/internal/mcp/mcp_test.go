package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/llm"
	"harness/internal/mcp"
	"harness/internal/tools"
)

// server builds cmd/mini-mcp once per test binary.
func server(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(os.TempDir(), "mini-mcp-test")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/mini-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build mini-mcp: %v\n%s", err, out)
	}
	return bin
}

func start(t *testing.T, args ...string) *mcp.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := mcp.Start(ctx, "todo", append([]string{server(t)}, args...), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestHandshakeAndList(t *testing.T) {
	c := start(t)
	if c.ServerName() != "mini-mcp" || c.Instructions() == "" {
		t.Errorf("serverInfo/instructions not read: %q %q", c.ServerName(), c.Instructions())
	}
	ts, err := c.ListTools(context.Background())
	if err != nil || len(ts) != 3 || ts[0].Name != "todo_add" || len(ts[0].InputSchema) == 0 {
		t.Fatalf("tools = %+v err = %v", ts, err)
	}
	if !ts[1].ReadOnly || ts[0].ReadOnly {
		t.Error("readOnlyHint annotations should be read")
	}
}

func TestHandshakeRejectsUnsupportedVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := `read line; printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2099-01-01"}}'; read line`
	c, err := mcp.Start(ctx, "incompatible", []string{"/bin/sh", "-c", script}, nil)
	if c != nil {
		c.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("expected unsupported version error, got %v", err)
	}
}

func TestCall(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	res, err := c.Call(ctx, "todo_add", json.RawMessage(`{"text":"write lesson 18"}`))
	if err != nil || res.IsError || res.Text != "added #1" {
		t.Errorf("add = %+v %v", res, err)
	}
	res, _ = c.Call(ctx, "todo_list", json.RawMessage(`{}`))
	if !strings.Contains(res.Text, "1. write lesson 18") {
		t.Errorf("list = %+v", res)
	}
	res, _ = c.Call(ctx, "todo_add", json.RawMessage(`{"text":""}`))
	if !res.IsError {
		t.Error("isError from the server must be preserved")
	}
	if _, err := c.Call(ctx, "nope", json.RawMessage(`{}`)); err == nil {
		t.Error("a JSON-RPC error must come back as a Go error")
	}
}

func TestServerCrashSurfacesError(t *testing.T) {
	c := start(t, "--crash-after", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Call(ctx, "todo_list", json.RawMessage(`{}`))
	_, err := c.Call(ctx, "todo_list", json.RawMessage(`{}`))
	if err == nil || ctx.Err() != nil {
		t.Errorf("a dead server must fail pending calls promptly, err = %v", err)
	}
}

func TestToolName(t *testing.T) {
	if got := mcp.ToolName("my server", "do.thing"); got != "mcp__my_server__do_thing" {
		t.Errorf("ToolName = %q", got)
	}
	long := mcp.ToolName(strings.Repeat("s", 40), strings.Repeat("t", 40))
	if len(long) > 64 || !strings.HasPrefix(long, "mcp__") {
		t.Errorf("names must fit provider limits (64 chars): %q (%d)", long, len(long))
	}
	if mcp.ToolName(strings.Repeat("s", 40), strings.Repeat("t", 40)+"a") == long {
		t.Error("truncated names must stay distinct")
	}
}

func TestRegisterLoaded(t *testing.T) {
	c := start(t)
	reg := tools.NewRegistry(&tools.Env{})
	n, err := mcp.Register(context.Background(), reg, "todo", c, false)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	res := reg.Execute(context.Background(), []llm.ToolCall{{ID: "1", Name: "mcp__todo__word_count", Args: json.RawMessage(`{"text":"a b c"}`)}}, nil, nil)
	if res[0].IsError || res[0].Content != "3" {
		t.Errorf("result = %+v", res[0])
	}
}

func TestDeferredSearchActivates(t *testing.T) {
	c := start(t, "--synthetic", "10")
	reg := tools.NewRegistry(&tools.Env{})
	if _, err := mcp.Register(context.Background(), reg, "todo", c, true); err != nil {
		t.Fatal(err)
	}
	names := func() string {
		var n []string
		for _, s := range reg.Specs() {
			n = append(n, s.Name)
		}
		return strings.Join(n, ",")
	}
	if names() != "tool_search" {
		t.Fatalf("deferred mode should expose only tool_search, got %s", names())
	}
	res := reg.Execute(context.Background(), []llm.ToolCall{{ID: "1", Name: "tool_search", Args: json.RawMessage(`{"query":"todo"}`)}}, nil, nil)
	if !strings.Contains(res[0].Content, "mcp__todo__todo_add") || strings.Contains(res[0].Content, "analytics") {
		t.Errorf("search result = %q", res[0].Content)
	}
	if !strings.Contains(names(), "mcp__todo__todo_add") || !strings.Contains(names(), "mcp__todo__todo_list") || strings.Contains(names(), "analytics") {
		t.Errorf("matching tools should now be registered: %s", names())
	}
}

func TestDeferredSearchSpansServers(t *testing.T) {
	a, b := start(t), start(t)
	reg := tools.NewRegistry(&tools.Env{})
	mcp.Register(context.Background(), reg, "one", a, true)
	mcp.Register(context.Background(), reg, "two", b, true)
	res := reg.Execute(context.Background(), []llm.ToolCall{{ID: "1", Name: "tool_search", Args: json.RawMessage(`{"query":"word"}`)}}, nil, nil)
	if !strings.Contains(res[0].Content, "mcp__one__word_count") || !strings.Contains(res[0].Content, "mcp__two__word_count") {
		t.Errorf("one tool_search must cover every deferred server: %q", res[0].Content)
	}
}
