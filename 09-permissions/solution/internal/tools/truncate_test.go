package tools_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"harness/internal/tools"
)

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestTruncateUnderLimitsUnchanged(t *testing.T) {
	s := lines(10)
	out, cut := tools.Truncate(s, 100, 1000, tools.TruncHead)
	if cut || out != s {
		t.Errorf("cut=%v out=%q", cut, out)
	}
}

func TestTruncateHeadKeepsBeginning(t *testing.T) {
	out, cut := tools.Truncate(lines(100), 10, 1<<20, tools.TruncHead)
	if !cut || !strings.HasPrefix(out, "line 1\n") || strings.Contains(out, "line 11\n") || !strings.Contains(out, "90 lines truncated") {
		t.Errorf("cut=%v out=%q", cut, out)
	}
}

func TestTruncateTailKeepsEnd(t *testing.T) {
	out, cut := tools.Truncate(lines(100), 10, 1<<20, tools.TruncTail)
	if !cut || !strings.HasSuffix(out, "line 100\n") || strings.Contains(out, "line 90\n") || !strings.Contains(out, "90 lines truncated") {
		t.Errorf("cut=%v out=%q", cut, out)
	}
}

func TestTruncateHeadTailKeepsBothEnds(t *testing.T) {
	out, cut := tools.Truncate(lines(100), 10, 1<<20, tools.TruncHeadTail)
	if !cut || !strings.HasPrefix(out, "line 1\n") || !strings.HasSuffix(out, "line 100\n") || strings.Contains(out, "line 50\n") {
		t.Errorf("cut=%v out=%q", cut, out)
	}
	if !strings.Contains(out, "90 lines truncated") {
		t.Errorf("marker missing: %q", out)
	}
}

func TestTruncateByBytes(t *testing.T) {
	s := strings.Repeat("x", 10000)
	out, cut := tools.Truncate(s, 100, 1000, tools.TruncHead)
	if !cut || len(out) > 1200 || !strings.Contains(out, "truncated") {
		t.Errorf("cut=%v len=%d", cut, len(out))
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := strings.Repeat("héllo wörld 日本語 ", 500)
	for _, mode := range []tools.TruncMode{tools.TruncHead, tools.TruncTail, tools.TruncHeadTail} {
		for _, limit := range []int{97, 98, 99, 100, 101} {
			out, cut := tools.Truncate(s, 1000, limit, mode)
			if !cut || !utf8.ValidString(out) {
				t.Errorf("mode %v limit %d: cut=%v valid=%v", mode, limit, cut, utf8.ValidString(out))
			}
		}
	}
}

func TestSaveOverflow(t *testing.T) {
	env := &tools.Env{Root: t.TempDir(), OverflowDir: t.TempDir()}
	path, err := tools.SaveOverflow(env, "full output")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "full output" {
		t.Errorf("saved %q", b)
	}
}
