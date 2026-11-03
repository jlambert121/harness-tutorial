package memory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/memory"
	"harness/internal/tools"
)

func run(t *testing.T, s *memory.Store, args map[string]any) tools.Result {
	t.Helper()
	b, _ := json.Marshal(args)
	return s.Tool().Run(context.Background(), &tools.Env{}, b)
}

const note = "---\nname: test-style\ndescription: Tests are table-driven with got/want and t.Run\ntype: feedback\n---\nUse table-driven tests.\n**Why:** the user asked for it.\n"

func TestMemoryToolCRUD(t *testing.T) {
	s := memory.Open(t.TempDir())
	if r := run(t, s, map[string]any{"command": "create", "path": "test-style.md", "content": note}); r.IsError {
		t.Fatal(r.Content)
	}
	if r := run(t, s, map[string]any{"command": "view", "path": "test-style.md"}); !strings.Contains(r.Content, "Use table-driven tests.") {
		t.Errorf("view = %q", r.Content)
	}
	if r := run(t, s, map[string]any{"command": "str_replace", "path": "test-style.md", "old_str": "table-driven tests.", "new_str": "table-driven tests with t.Run."}); r.IsError {
		t.Fatal(r.Content)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir(), "test-style.md"))
	if !strings.Contains(string(b), "with t.Run.") {
		t.Errorf("file = %q", b)
	}
	if r := run(t, s, map[string]any{"command": "str_replace", "path": "test-style.md", "old_str": "nothing like this", "new_str": "x"}); !r.IsError {
		t.Error("str_replace with a missing old_str must fail")
	}
	if r := run(t, s, map[string]any{"command": "delete", "path": "test-style.md"}); r.IsError {
		t.Fatal(r.Content)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "test-style.md")); err == nil {
		t.Error("delete did not remove the file")
	}
}

func TestIndexMaintained(t *testing.T) {
	s := memory.Open(t.TempDir())
	run(t, s, map[string]any{"command": "create", "path": "test-style.md", "content": note})
	run(t, s, map[string]any{"command": "create", "path": "deploy.md", "content": "---\nname: deploy\ndescription: Deploys go through make release\ntype: project\n---\nRun make release.\n"})
	idx, _ := os.ReadFile(filepath.Join(s.Dir(), "MEMORY.md"))
	for _, want := range []string{"- [test-style](test-style.md) — Tests are table-driven with got/want and t.Run", "- [deploy](deploy.md) — Deploys go through make release"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("MEMORY.md missing %q:\n%s", want, idx)
		}
	}
	run(t, s, map[string]any{"command": "delete", "path": "deploy.md"})
	idx, _ = os.ReadFile(filepath.Join(s.Dir(), "MEMORY.md"))
	if strings.Contains(string(idx), "deploy") {
		t.Errorf("deleted memories must leave the index:\n%s", idx)
	}
}

func TestCreateRequiresFrontmatter(t *testing.T) {
	s := memory.Open(t.TempDir())
	r := run(t, s, map[string]any{"command": "create", "path": "x.md", "content": "no frontmatter"})
	if !r.IsError || !strings.Contains(r.Content, "description") {
		t.Errorf("a memory needs name and description so the index can describe it: %+v", r)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	base := t.TempDir()
	s := memory.Open(filepath.Join(base, "memory"))
	for _, p := range []string{"../escape.md", "/etc/passwd", "a/../../escape.md", "MEMORY.md"} {
		r := run(t, s, map[string]any{"command": "create", "path": p, "content": note})
		if !r.IsError {
			t.Errorf("create %q should be rejected", p)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "escape.md")); err == nil {
		t.Error("a memory escaped its directory")
	}
}

func TestViewListsMemories(t *testing.T) {
	s := memory.Open(t.TempDir())
	if r := run(t, s, map[string]any{"command": "view"}); r.IsError || !strings.Contains(r.Content, "no memories") {
		t.Errorf("empty view = %+v", r)
	}
	run(t, s, map[string]any{"command": "create", "path": "test-style.md", "content": note})
	if r := run(t, s, map[string]any{"command": "view"}); !strings.Contains(r.Content, "test-style.md") || !strings.Contains(r.Content, "got/want") {
		t.Errorf("view = %q", r.Content)
	}
}

func TestRecall(t *testing.T) {
	s := memory.Open(t.TempDir())
	if s.Recall(200, 25000) != "" {
		t.Error("no memories → no recall block")
	}
	run(t, s, map[string]any{"command": "create", "path": "test-style.md", "content": note})
	r := s.Recall(200, 25000)
	if !strings.Contains(r, "<memory") || !strings.Contains(r, "test-style.md") || strings.Contains(r, "Use table-driven tests.") {
		t.Errorf("recall should carry the index, not the topic bodies:\n%s", r)
	}
	var long strings.Builder
	for i := 0; i < 500; i++ {
		long.WriteString("- [x](x.md) — line\n")
	}
	os.WriteFile(filepath.Join(s.Dir(), "MEMORY.md"), []byte(long.String()), 0o644)
	if r := s.Recall(200, 25000); strings.Count(r, "- [x]") != 200 || !strings.Contains(r, "truncated") {
		t.Errorf("recall must stop at 200 lines and say so; lines = %d", strings.Count(r, "- [x]"))
	}
}

func TestStrReplaceKeepsFrontmatter(t *testing.T) {
	s := memory.Open(t.TempDir())
	run(t, s, map[string]any{"command": "create", "path": "test-style.md", "content": note})
	r := run(t, s, map[string]any{"command": "str_replace", "path": "test-style.md", "old_str": "---\nname: test-style\n", "new_str": ""})
	if !r.IsError {
		t.Error("an edit that breaks the frontmatter must be refused, or the memory silently drops out of the index")
	}
	idx, _ := os.ReadFile(filepath.Join(s.Dir(), "MEMORY.md"))
	if !strings.Contains(string(idx), "test-style") {
		t.Error("the memory must still be indexed")
	}
}

func TestIndexNameAnyCase(t *testing.T) {
	s := memory.Open(t.TempDir())
	for _, p := range []string{"MEMORY", "memory.md", "Memory.MD"} {
		if r := run(t, s, map[string]any{"command": "create", "path": p, "content": note}); !r.IsError {
			t.Errorf("%q would overwrite the index on a case-insensitive filesystem", p)
		}
	}
}
