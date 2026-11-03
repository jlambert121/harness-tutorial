package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"harness/internal/tools"
)

func bashEnv(t *testing.T) *tools.Env {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	return &tools.Env{Root: root, OverflowDir: t.TempDir()}
}

func bash(t *testing.T, ctx context.Context, env *tools.Env, args map[string]any) tools.Result {
	t.Helper()
	b, _ := json.Marshal(args)
	return tools.Bash().Run(ctx, env, b)
}

func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

func pgidOf(t *testing.T, res tools.Result) int {
	t.Helper()
	d, ok := res.Details.(tools.BashDetails)
	if !ok || d.PID == 0 {
		t.Fatalf("Details should be tools.BashDetails with the PID, got %#v", res.Details)
	}
	return d.PID
}

func TestBashExitCodeAndOutput(t *testing.T) {
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "echo out; echo err >&2; exit 3"})
	if !res.IsError {
		t.Error("non-zero exit should be reported as an error result")
	}
	for _, want := range []string{"out", "err", "exit code: 3"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("content %q missing %q", res.Content, want)
		}
	}
	if d := res.Details.(tools.BashDetails); d.ExitCode != 3 {
		t.Errorf("details = %+v", d)
	}
}

func TestBashSuccess(t *testing.T) {
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "printf hello"})
	if res.IsError || !strings.HasPrefix(res.Content, "hello") || !strings.Contains(res.Content, "exit code: 0") {
		t.Errorf("res = %+v", res)
	}
}

func TestBashRunsInWorkspace(t *testing.T) {
	env := bashEnv(t)
	res := bash(t, context.Background(), env, map[string]any{"command": "pwd -P"})
	root, _ := filepath.EvalSymlinks(env.Root)
	if !strings.HasPrefix(res.Content, root) {
		t.Errorf("pwd = %q, want workspace %s", res.Content, root)
	}
}

func TestBashTimeoutKillsGroup(t *testing.T) {
	start := time.Now()
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "sleep 30 & sleep 30; wait", "timeout_ms": 500})
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("timeout took %v", el)
	}
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("res = %+v", res)
	}
	if pg := pgidOf(t, res); groupAlive(pg) {
		t.Errorf("process group %d still has live processes", pg)
	}
}

func TestBashKillsChildThatIgnoresSIGTERM(t *testing.T) {
	start := time.Now()
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "trap '' TERM; sleep 30", "timeout_ms": 300})
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("took %v; SIGKILL should follow SIGTERM after a short grace period", el)
	}
	if pg := pgidOf(t, res); groupAlive(pg) {
		t.Errorf("process group %d survived", pg)
	}
}

func TestBashCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	start := time.Now()
	res := bash(t, ctx, bashEnv(t), map[string]any{"command": "sleep 30"})
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("cancel took %v", el)
	}
	if !res.IsError || !strings.Contains(res.Content, "cancelled") {
		t.Errorf("res = %+v", res)
	}
	if pg := pgidOf(t, res); groupAlive(pg) {
		t.Errorf("process group %d still alive", pg)
	}
}

func TestBashBackgroundChildDoesNotHang(t *testing.T) {
	start := time.Now()
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "sleep 30 & echo started"})
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("a background child holding stdout made the call hang for %v", el)
	}
	if !strings.Contains(res.Content, "started") || res.IsError || !strings.Contains(res.Content, "exit code: 0") {
		t.Errorf("res = %+v; the shell exited 0 even though a child held stdout", res)
	}
	if pg := pgidOf(t, res); groupAlive(pg) {
		t.Errorf("background child left running in group %d", pg)
	}
}

func TestBashStdinIsEmpty(t *testing.T) {
	start := time.Now()
	bash(t, context.Background(), bashEnv(t), map[string]any{"command": "cat; read x; echo done"})
	if time.Since(start) > 3*time.Second {
		t.Error("commands that read stdin must get EOF, not wait for input")
	}
}

func TestBashHugeOutputBounded(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res := bash(t, context.Background(), bashEnv(t), map[string]any{"command": "yes 'line of output' | head -c 50000000; echo END"})
	runtime.ReadMemStats(&after)
	if grew := int64(after.TotalAlloc - before.TotalAlloc); grew > 64<<20 {
		t.Errorf("allocated %d MB for 50 MB of output; the buffer must be bounded", grew>>20)
	}
	d := res.Details.(tools.BashDetails)
	if d.Dropped < 40_000_000 || !strings.Contains(res.Content, "END") || !strings.Contains(res.Content, "bytes omitted") {
		t.Errorf("dropped=%d; head and tail must be kept with a marker; tail=%q", d.Dropped, res.Content[max(0, len(res.Content)-200):])
	}
}

func TestBashCwdPersists(t *testing.T) {
	env := bashEnv(t)
	bash(t, context.Background(), env, map[string]any{"command": "cd sub"})
	res := bash(t, context.Background(), env, map[string]any{"command": "pwd -P"})
	if !strings.Contains(res.Content, filepath.Join("", "sub")) {
		t.Errorf("cwd did not persist: %q", res.Content)
	}
}

func TestBashCwdOutsideRootResets(t *testing.T) {
	env := bashEnv(t)
	res := bash(t, context.Background(), env, map[string]any{"command": "cd /"})
	if !strings.Contains(res.Content, "reset") {
		t.Errorf("leaving the workspace should be reported: %q", res.Content)
	}
	res = bash(t, context.Background(), env, map[string]any{"command": "pwd -P"})
	root, _ := filepath.EvalSymlinks(env.Root)
	if !strings.HasPrefix(res.Content, root) {
		t.Errorf("cwd = %q, want reset to %s", res.Content, root)
	}
}

func TestBashWorkdirArg(t *testing.T) {
	env := bashEnv(t)
	res := bash(t, context.Background(), env, map[string]any{"command": "pwd -P", "workdir": "sub"})
	if !strings.Contains(res.Content, "sub") {
		t.Errorf("workdir ignored: %q", res.Content)
	}
	res = bash(t, context.Background(), env, map[string]any{"command": "pwd", "workdir": "/etc"})
	if !res.IsError || !strings.Contains(res.Content, "outside the workspace") {
		t.Errorf("workdir outside root accepted: %+v", res)
	}
}

func TestConfirmDeniedReturnsError(t *testing.T) {
	env := bashEnv(t)
	var asked string
	env.Confirm = func(ctx context.Context, command string) bool { asked = command; return false }
	res := bash(t, context.Background(), env, map[string]any{"command": "touch should-not-exist"})
	if asked != "touch should-not-exist" || !res.IsError || !strings.Contains(res.Content, "denied") {
		t.Errorf("asked=%q res=%+v", asked, res)
	}
	if _, err := os.Stat(filepath.Join(env.Root, "should-not-exist")); err == nil {
		t.Error("denied command ran")
	}
}

func TestHeadTail(t *testing.T) {
	h := tools.NewHeadTail(10)
	h.Write([]byte("0123456789abcdefghij"))
	h.Write([]byte("KLMNO"))
	if h.Dropped() != 15 {
		t.Errorf("dropped = %d", h.Dropped())
	}
	s := h.String()
	if !strings.HasPrefix(s, "01234") || !strings.HasSuffix(s, "KLMNO") || !strings.Contains(s, "15 bytes omitted") {
		t.Errorf("String() = %q", s)
	}
	small := tools.NewHeadTail(100)
	small.Write([]byte("short"))
	if small.String() != "short" || small.Dropped() != 0 {
		t.Errorf("small = %q", small.String())
	}
}
