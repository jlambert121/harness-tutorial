package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits shared by pi and opencode.
const (
	MaxLines     = 2000
	MaxBytes     = 50 * 1024
	MaxLineChars = 2000
)

type TruncMode int

const (
	TruncHead     TruncMode = iota // keep the beginning (file reads, search results)
	TruncTail                      // keep the end (command output: errors are usually last)
	TruncHeadTail                  // keep both ends (Codex's default for tool output)
)

// Truncate cuts s to at most maxLines lines and roughly maxBytes bytes,
// never splitting a UTF-8 sequence, and inserts a marker saying what was cut.
func Truncate(s string, maxLines, maxBytes int, mode TruncMode) (string, bool) {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		return s, false
	}
	switch mode {
	case TruncHead:
		head := take(lines, maxLines, maxBytes, false)
		return strings.Join(head, "") + marker(s, lines, len(head)), true
	case TruncTail:
		tail := take(lines, maxLines, maxBytes, true)
		return marker(s, lines, len(tail)) + strings.Join(tail, ""), true
	default:
		head := take(lines, maxLines/2, maxBytes/2, false)
		tail := take(lines[len(head):], maxLines-maxLines/2, maxBytes-maxBytes/2, true)
		return strings.Join(head, "") + marker(s, lines, len(head)+len(tail)) + strings.Join(tail, ""), true
	}
}

// take returns up to n lines from the front (or back) of lines within budget
// bytes. A single line larger than the budget is cut on a rune boundary.
func take(lines []string, n, budget int, fromEnd bool) []string {
	var out []string
	used := 0
	for i := 0; i < len(lines) && len(out) < n; i++ {
		l := lines[i]
		if fromEnd {
			l = lines[len(lines)-1-i]
		}
		if used+len(l) > budget {
			if len(out) == 0 {
				out = append(out, clip(l, budget, fromEnd))
			}
			break
		}
		out = append(out, l)
		used += len(l)
	}
	if fromEnd {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

func clip(s string, n int, fromEnd bool) string {
	if fromEnd {
		i := len(s) - n
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		return s[i:]
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n"
}

func marker(s string, lines []string, kept int) string {
	return fmt.Sprintf("\n… %d lines truncated (%d lines, %d bytes total) …\n", len(lines)-kept, len(lines), len(s))
}

// SaveOverflow writes full output to the overflow directory and returns its path.
func SaveOverflow(env *Env, s string) (string, error) {
	dir := env.overflowDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, time.Now().Format("20060102-150405-")+"*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return f.Name(), err
}

func (e *Env) overflowDir() string {
	if e.OverflowDir != "" {
		return e.OverflowDir
	}
	return filepath.Join(os.TempDir(), "harness-overflow")
}
