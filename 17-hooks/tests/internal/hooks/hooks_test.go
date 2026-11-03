package hooks_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/hooks"
)

func runner(t *testing.T, hs ...hooks.Hook) *hooks.Runner {
	t.Helper()
	return hooks.New(t.TempDir(), hs)
}

func TestMatch(t *testing.T) {
	cases := []struct {
		matcher, tool string
		want          bool
	}{
		{"", "anything", true},
		{"*", "edit", true},
		{"edit", "edit", true},
		{"edit", "edit_notebook", false},
		{"edit|write", "write", true},
		{"mcp__.*", "mcp__github__search", true},
		{"mcp__.*", "bash", false},
	}
	for _, c := range cases {
		if got := hooks.Match(c.matcher, c.tool); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.matcher, c.tool, got)
		}
	}
}

func TestExit2Blocks(t *testing.T) {
	r := runner(t, hooks.Hook{Event: hooks.PreToolUse, Matcher: "bash", Command: `echo "no pushing from agents" >&2; exit 2`})
	out := r.Run(context.Background(), hooks.PreToolUse, "bash", map[string]any{"args": map[string]any{"command": "git push"}})
	if !out.Block || out.Reason != "no pushing from agents" {
		t.Errorf("out = %+v", out)
	}
	if out := r.Run(context.Background(), hooks.PreToolUse, "read", nil); out.Block {
		t.Error("hook must only run for matching tools")
	}
}

func TestPayloadOnStdin(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "payload.json")
	r := runner(t, hooks.Hook{Event: hooks.PostToolUse, Command: "cat > " + got})
	r.Run(context.Background(), hooks.PostToolUse, "edit", map[string]any{"args": map[string]any{"path": "a.go"}, "result": "Edited a.go"})
	b, _ := os.ReadFile(got)
	var p map[string]any
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("stdin was not JSON: %q", b)
	}
	if p["event"] != "post_tool_use" || p["tool"] != "edit" || p["result"] != "Edited a.go" || p["cwd"] == nil {
		t.Errorf("payload = %v", p)
	}
}

func TestJSONOutput(t *testing.T) {
	r := runner(t, hooks.Hook{Event: hooks.PreToolUse, Matcher: "bash",
		Command: `echo '{"updated_args": {"command": "go test -count=1 ./..."}, "additional_context": "tests run uncached"}'`})
	out := r.Run(context.Background(), hooks.PreToolUse, "bash", map[string]any{"args": map[string]any{"command": "go test ./..."}})
	if out.Block || string(out.UpdatedArgs) != `{"command": "go test -count=1 ./..."}` || out.AdditionalContext != "tests run uncached" {
		t.Errorf("out = %+v", out)
	}
	r = runner(t, hooks.Hook{Event: hooks.PreToolUse, Command: `echo '{"decision": "block", "reason": "read-only today"}'`})
	if out := r.Run(context.Background(), hooks.PreToolUse, "edit", nil); !out.Block || out.Reason != "read-only today" {
		t.Errorf("decision block = %+v", out)
	}
}

func TestPlainStdoutIsContextForPromptEvents(t *testing.T) {
	r := runner(t, hooks.Hook{Event: hooks.SessionStart, Command: `echo "branch: main"`})
	if out := r.Run(context.Background(), hooks.SessionStart, "", nil); out.AdditionalContext != "branch: main" {
		t.Errorf("out = %+v", out)
	}
}

func TestOtherExitCodesDoNotBlock(t *testing.T) {
	r := runner(t, hooks.Hook{Event: hooks.PreToolUse, Command: `echo broken >&2; exit 1`})
	out := r.Run(context.Background(), hooks.PreToolUse, "edit", nil)
	if out.Block || !strings.Contains(out.Error, "broken") {
		t.Errorf("a failing hook must be reported but not block: %+v", out)
	}
}

func TestTimeout(t *testing.T) {
	r := runner(t, hooks.Hook{Event: hooks.Stop, Command: "sleep 30", TimeoutSec: 1})
	start := time.Now()
	out := r.Run(context.Background(), hooks.Stop, "", nil)
	if time.Since(start) > 5*time.Second || !strings.Contains(out.Error, "timed out") {
		t.Errorf("out = %+v after %v", out, time.Since(start))
	}
}

func TestMultipleHooksCombine(t *testing.T) {
	r := runner(t,
		hooks.Hook{Event: hooks.PostToolUse, Command: `echo '{"additional_context": "one"}'`},
		hooks.Hook{Event: hooks.PostToolUse, Command: `echo '{"additional_context": "two"}'`},
	)
	if out := r.Run(context.Background(), hooks.PostToolUse, "edit", nil); out.AdditionalContext != "one\ntwo" {
		t.Errorf("out = %+v", out)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	os.WriteFile(path, []byte(`{"hooks":[{"event":"post_tool_use","matcher":"edit|write","command":"gofmt -l .","timeout_sec":30}]}`), 0o644)
	hs, err := hooks.Load(path, filepath.Join(dir, "missing.json"))
	if err != nil || len(hs) != 1 || hs[0].Matcher != "edit|write" || hs[0].TimeoutSec != 30 {
		t.Errorf("hs = %+v err = %v", hs, err)
	}
	os.WriteFile(path, []byte(`{"hooks":[{"event":"after_lunch","command":"x"}]}`), 0o644)
	if _, err := hooks.Load(path); err == nil {
		t.Error("unknown events must be rejected at load time")
	}
}
