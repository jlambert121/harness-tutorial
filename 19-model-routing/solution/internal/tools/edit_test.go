package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/llm"
	"harness/internal/tools"
)

// editWorkspace registers read, edit, and write with read tracking enabled.
func editWorkspace(t *testing.T, files map[string]string) (*tools.Env, *tools.Registry) {
	t.Helper()
	env, r := workspace(t, files)
	env.Reads = tools.NewReadTracker()
	r.Register(tools.Edit())
	r.Register(tools.Write())
	return env, r
}

func readFile(t *testing.T, env *tools.Env, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.Root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustOK(t *testing.T, res llm.ToolResult) {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
}

func TestEditExactUnique(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": src})
	mustOK(t, call(t, r, "read", map[string]any{"path": "a.go"}))
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	mustOK(t, res)
	if got := readFile(t, env, "a.go"); !strings.Contains(got, "x := 2") || strings.Contains(got, "x := 1") {
		t.Errorf("file = %q", got)
	}
}

func TestEditReturnsUnifiedDiff(t *testing.T) {
	_, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	if !strings.Contains(res.Content, "-\tx := 1") || !strings.Contains(res.Content, "+\tx := 2") || !strings.Contains(res.Content, "@@") {
		t.Errorf("result should include a unified diff: %q", res.Content)
	}
}

func TestEditAmbiguousError(t *testing.T) {
	_, r := editWorkspace(t, map[string]string{"a.go": "a := 1\nb := 1\n"})
	call(t, r, "read", map[string]any{"path": "a.go"})
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": " := 1", "new_string": " := 2"})
	if !res.IsError || !strings.Contains(res.Content, "Found 2 matches") || !strings.Contains(res.Content, "more surrounding context") || !strings.Contains(res.Content, "replace_all") {
		t.Errorf("res = %+v", res)
	}
}

func TestEditReplaceAll(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": "a := 1\nb := 1\n"})
	call(t, r, "read", map[string]any{"path": "a.go"})
	mustOK(t, call(t, r, "edit", map[string]any{"path": "a.go", "old_string": " := 1", "new_string": " := 2", "replace_all": true}))
	if got := readFile(t, env, "a.go"); got != "a := 2\nb := 2\n" {
		t.Errorf("file = %q", got)
	}
}

func TestEditFallbackReportsStrategy(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "\tx := 1   ", "new_string": "\tx := 3"})
	mustOK(t, res)
	if !strings.Contains(res.Content, "trailing_ws") {
		t.Errorf("result should say a fallback strategy was used: %q", res.Content)
	}
	if got := readFile(t, env, "a.go"); !strings.Contains(got, "\tx := 3\n\tif") {
		t.Errorf("file = %q", got)
	}
}

func TestEditNotFoundIsActionable(t *testing.T) {
	_, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "nothing like this", "new_string": "x"})
	if !res.IsError || !strings.Contains(res.Content, "Could not find") || !strings.Contains(res.Content, "Read the file again") {
		t.Errorf("res = %+v", res)
	}
}

func TestEditRejectsNoopAndEmpty(t *testing.T) {
	_, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	if res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 1"}); !res.IsError {
		t.Error("identical old/new should be rejected")
	}
	if res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "", "new_string": "x"}); !res.IsError || !strings.Contains(res.Content, "write") {
		t.Errorf("empty old_string should point to the write tool: %+v", res)
	}
}

func TestEditRequiresPriorRead(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": src})
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	if !res.IsError || !strings.Contains(res.Content, "read the file first") {
		t.Errorf("res = %+v", res)
	}
	if got := readFile(t, env, "a.go"); got != src {
		t.Error("file changed despite refused edit")
	}
}

func TestEditStaleRead(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	path := filepath.Join(env.Root, "a.go")
	os.WriteFile(path, []byte(src+"// changed by someone else\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	res := call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	if !res.IsError || !strings.Contains(res.Content, "modified since") {
		t.Errorf("res = %+v", res)
	}
}

func TestConsecutiveEditsDoNotNeedReread(t *testing.T) {
	_, r := editWorkspace(t, map[string]string{"a.go": src})
	call(t, r, "read", map[string]any{"path": "a.go"})
	mustOK(t, call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"}))
	mustOK(t, call(t, r, "edit", map[string]any{"path": "a.go", "old_string": "x > 0", "new_string": "x > 1"}))
}

func TestPreservesCRLFAndBOM(t *testing.T) {
	content := "\uFEFFline one\r\nline two\r\nline three\r\n"
	env, r := editWorkspace(t, map[string]string{"w.txt": content})
	call(t, r, "read", map[string]any{"path": "w.txt"})
	mustOK(t, call(t, r, "edit", map[string]any{"path": "w.txt", "old_string": "line two\nline three", "new_string": "line 2\nline 3"}))
	if got := readFile(t, env, "w.txt"); got != "\uFEFFline one\r\nline 2\r\nline 3\r\n" {
		t.Errorf("file = %q; BOM and CRLF must survive an LF-only edit", got)
	}
}

func TestWriteCreatesAndRequiresReadToOverwrite(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"old.txt": "keep me"})
	mustOK(t, call(t, r, "write", map[string]any{"path": "new/dir/f.txt", "content": "hello\n"}))
	if readFile(t, env, "new/dir/f.txt") != "hello\n" {
		t.Error("write did not create the file with parents")
	}
	res := call(t, r, "write", map[string]any{"path": "old.txt", "content": "clobbered"})
	if !res.IsError || readFile(t, env, "old.txt") != "keep me" {
		t.Errorf("overwriting an unread file must be refused: %+v", res)
	}
	call(t, r, "read", map[string]any{"path": "old.txt"})
	mustOK(t, call(t, r, "write", map[string]any{"path": "old.txt", "content": "replaced"}))
}

func TestWorkspaceRejectsSymlinkEscape(t *testing.T) {
	env, r := editWorkspace(t, map[string]string{"a.go": src})
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(env.Root, "link"))
	for _, p := range []string{"link/secret.txt", "../" + filepath.Base(outside) + "/secret.txt", filepath.Join(outside, "secret.txt"), "link/new.txt"} {
		if res := call(t, r, "read", map[string]any{"path": p}); !res.IsError || !strings.Contains(res.Content, "outside the workspace") {
			t.Errorf("read %s = %+v", p, res)
		}
	}
	if res := call(t, r, "write", map[string]any{"path": "link/new.txt", "content": "x"}); !res.IsError {
		t.Errorf("write through symlink escaped the workspace: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Error("file created outside the workspace")
	}
}

func TestResolveAllowsNewFilesInsideRoot(t *testing.T) {
	env, _ := editWorkspace(t, nil)
	p, err := env.Resolve("brand/new/file.go")
	if err != nil || !strings.HasSuffix(p, filepath.Join("brand", "new", "file.go")) {
		t.Errorf("p=%q err=%v", p, err)
	}
}
