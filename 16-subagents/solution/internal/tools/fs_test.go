package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/llm"
	"harness/internal/tools"
)

// workspace builds a temp tree from path → content.
func workspace(t *testing.T, files map[string]string) (*tools.Env, *tools.Registry) {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	env := &tools.Env{Root: root, OverflowDir: t.TempDir()}
	r := tools.NewRegistry(env)
	for _, tool := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls()} {
		r.Register(tool)
	}
	return env, r
}

func call(t *testing.T, r *tools.Registry, name string, args any) llm.ToolResult {
	t.Helper()
	b, _ := json.Marshal(args)
	return r.Execute(context.Background(), []llm.ToolCall{{ID: "1", Name: name, Args: b}}, nil, nil)[0]
}

func TestReadLineNumbersAndPaging(t *testing.T) {
	_, r := workspace(t, map[string]string{"f.txt": "a\nb\nc\nd\ne\nf\n"})
	res := call(t, r, "read", map[string]any{"path": "f.txt", "offset": 3, "limit": 2})
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "3\tc\n") || !strings.Contains(res.Content, "4\td\n") || strings.Contains(res.Content, "\te\n") {
		t.Errorf("content = %q; want lines 3-4 numbered as <n>\\t<line>", res.Content)
	}
	if !strings.Contains(res.Content, "offset=5") {
		t.Errorf("content = %q; footer should say how to continue (offset=5)", res.Content)
	}
}

func TestReadWholeFileHasNoFooter(t *testing.T) {
	_, r := workspace(t, map[string]string{"f.txt": "a\nb\n"})
	res := call(t, r, "read", map[string]any{"path": "f.txt"})
	if strings.Contains(res.Content, "offset=") {
		t.Errorf("unexpected continuation footer: %q", res.Content)
	}
}

func TestReadDefaultLimit2000(t *testing.T) {
	_, r := workspace(t, map[string]string{"big.txt": lines(2500)})
	res := call(t, r, "read", map[string]any{"path": "big.txt"})
	if !strings.Contains(res.Content, "2000\tline 2000\n") || strings.Contains(res.Content, "line 2001\n") || !strings.Contains(res.Content, "offset=2001") {
		t.Errorf("read should stop at 2000 lines with a continuation hint; tail = %q", res.Content[len(res.Content)-200:])
	}
}

func TestReadLongLinesClipped(t *testing.T) {
	_, r := workspace(t, map[string]string{"min.js": strings.Repeat("x", 5000) + "\n"})
	res := call(t, r, "read", map[string]any{"path": "min.js"})
	if len(res.Content) > 2500 || !strings.Contains(res.Content, "line truncated") {
		t.Errorf("len=%d; lines over 2000 chars should be clipped with a note", len(res.Content))
	}
}

func TestReadRefusesBinary(t *testing.T) {
	_, r := workspace(t, map[string]string{"img.bin": "\x89PNG\r\n\x1a\n\x00\x00\x00"})
	res := call(t, r, "read", map[string]any{"path": "img.bin"})
	if !res.IsError || !strings.Contains(res.Content, "binary") {
		t.Errorf("res = %+v", res)
	}
}

func TestReadValidationError(t *testing.T) {
	_, r := workspace(t, nil)
	res := call(t, r, "read", map[string]any{"file": "x"})
	if !res.IsError || !strings.Contains(res.Content, `unknown field "file"`) {
		t.Errorf("res = %+v", res)
	}
}

func TestGlob(t *testing.T) {
	_, r := workspace(t, map[string]string{
		"a.go": "", "pkg/b.go": "", "pkg/sub/c.go": "", "pkg/d.txt": "",
		"vendor/v.go": "", ".gitignore": "vendor/\n", ".git/config": "",
	})
	res := call(t, r, "glob", map[string]any{"pattern": "**/*.go"})
	got := strings.Fields(res.Content)
	want := []string{"a.go", "pkg/b.go", "pkg/sub/c.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("glob **/*.go = %v, want %v (gitignored and .git excluded, sorted)", got, want)
	}
	res = call(t, r, "glob", map[string]any{"pattern": "pkg/*.go"})
	if strings.TrimSpace(res.Content) != "pkg/b.go" {
		t.Errorf("glob pkg/*.go = %q", res.Content)
	}
}

func TestGlobCap100(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 150; i++ {
		files[fmt.Sprintf("f%03d.txt", i)] = ""
	}
	_, r := workspace(t, files)
	res := call(t, r, "glob", map[string]any{"pattern": "*.txt"})
	if n := strings.Count(res.Content, ".txt\n"); n != 100 || !strings.Contains(res.Content, "150 matches") {
		t.Errorf("got %d lines; want 100 plus a note that there are 150 matches: %q", n, res.Content[len(res.Content)-120:])
	}
}

func TestGrepContentAndFiles(t *testing.T) {
	_, r := workspace(t, map[string]string{
		"a.go":        "package a\nfunc Hello() {}\n",
		"b.go":        "package b\n// hello there\n",
		"vendor/c.go": "func Hello() {}\n",
		".gitignore":  "vendor/\n",
	})
	res := call(t, r, "grep", map[string]any{"pattern": "Hello"})
	if strings.TrimSpace(res.Content) != "a.go" {
		t.Errorf("files mode = %q", res.Content)
	}
	res = call(t, r, "grep", map[string]any{"pattern": "(?i)hello", "mode": "content"})
	if !strings.Contains(res.Content, "a.go:2:func Hello() {}") || !strings.Contains(res.Content, "b.go:2:// hello there") || strings.Contains(res.Content, "vendor") {
		t.Errorf("content mode = %q", res.Content)
	}
	res = call(t, r, "grep", map[string]any{"pattern": "package", "include": "b.*"})
	if strings.TrimSpace(res.Content) != "b.go" {
		t.Errorf("include filter = %q", res.Content)
	}
}

func TestGrepBadRegexIsActionable(t *testing.T) {
	_, r := workspace(t, map[string]string{"a.go": "fmt.Printf(\"x\")\n"})
	res := call(t, r, "grep", map[string]any{"pattern": "fmt.Printf("})
	if !res.IsError || !strings.Contains(res.Content, "regular expression") || !strings.Contains(res.Content, `\(`) {
		t.Errorf("res = %+v; error should explain the regex problem and suggest escaping", res)
	}
}

func TestGrepNoMatches(t *testing.T) {
	_, r := workspace(t, map[string]string{"a.go": "package a\n"})
	res := call(t, r, "grep", map[string]any{"pattern": "zzz"})
	if res.IsError || !strings.Contains(res.Content, "No matches") {
		t.Errorf("res = %+v", res)
	}
}

func TestLs(t *testing.T) {
	_, r := workspace(t, map[string]string{"a.go": "", "pkg/b.go": "", "node_modules/x.js": "", ".gitignore": "node_modules/\n"})
	res := call(t, r, "ls", map[string]any{})
	if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "pkg/") || strings.Contains(res.Content, "node_modules") {
		t.Errorf("ls = %q", res.Content)
	}
}

func TestRegistryTruncatesOversizedResults(t *testing.T) {
	env, r := workspace(t, nil)
	r.Register(tools.Func{
		Def: llm.ToolSpec{Name: "flood", Schema: json.RawMessage(`{"type":"object"}`)},
		Fn: func(context.Context, *tools.Env, json.RawMessage) tools.Result {
			return tools.Result{Content: lines(5000)}
		},
	})
	res := call(t, r, "flood", map[string]any{})
	if strings.Count(res.Content, "\n") > tools.MaxLines+10 || !strings.Contains(res.Content, "truncated") {
		t.Fatalf("registry must cap every tool result; got %d lines", strings.Count(res.Content, "\n"))
	}
	entries, _ := os.ReadDir(env.OverflowDir)
	if len(entries) != 1 || !strings.Contains(res.Content, filepath.Join(env.OverflowDir, entries[0].Name())) {
		t.Errorf("full output should be saved and its path given to the model; content tail = %q", res.Content[len(res.Content)-300:])
	}
}
