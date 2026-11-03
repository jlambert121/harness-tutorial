package tools_test

import (
	"strings"
	"testing"

	"harness/internal/hooks"
)

func TestPreToolUseBlocksAndRewrites(t *testing.T) {
	env, r := workspace(t, map[string]string{"a.txt": "alpha", "b.txt": "beta"})
	env.Hooks = hooks.New(env.Root, []hooks.Hook{
		{Event: hooks.PreToolUse, Matcher: "read", Command: `grep -q '"path":"a.txt"' && echo '{"updated_args": {"path": "b.txt"}}'; exit 0`},
		{Event: hooks.PreToolUse, Matcher: "ls", Command: `echo "listing is disabled" >&2; exit 2`},
	})
	if res := call(t, r, "read", map[string]any{"path": "a.txt"}); !strings.Contains(res.Content, "beta") {
		t.Errorf("updated_args should replace the arguments: %q", res.Content)
	}
	if res := call(t, r, "ls", map[string]any{}); !res.IsError || !strings.Contains(res.Content, "listing is disabled") {
		t.Errorf("exit 2 must block with the hook's reason: %+v", res)
	}
}

func TestPostToolUseAddsContext(t *testing.T) {
	env, r := workspace(t, map[string]string{"a.txt": "alpha"})
	env.Hooks = hooks.New(env.Root, []hooks.Hook{
		{Event: hooks.PostToolUse, Matcher: "read", Command: `echo '{"additional_context": "reminder: a.txt is generated"}'`},
	})
	res := call(t, r, "read", map[string]any{"path": "a.txt"})
	if !strings.Contains(res.Content, "alpha") || !strings.Contains(res.Content, "<hook_output>") || !strings.Contains(res.Content, "a.txt is generated") {
		t.Errorf("result = %q", res.Content)
	}
}

func TestHookFailureIsReported(t *testing.T) {
	env, r := workspace(t, map[string]string{"a.txt": "alpha"})
	env.Hooks = hooks.New(env.Root, []hooks.Hook{{Event: hooks.PostToolUse, Matcher: "read", Command: `echo "linter crashed" >&2; exit 1`}})
	res := call(t, r, "read", map[string]any{"path": "a.txt"})
	if res.IsError || !strings.Contains(res.Content, "alpha") || !strings.Contains(res.Content, "linter crashed") {
		t.Errorf("a failing hook must not block, but must not be invisible either: %+v", res)
	}
}
