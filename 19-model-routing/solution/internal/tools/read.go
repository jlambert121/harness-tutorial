package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

type readArgs struct {
	Path   string `json:"path" desc:"File path, relative to the workspace root or absolute"`
	Offset int    `json:"offset,omitempty" desc:"1-based line number to start from (default 1)"`
	Limit  int    `json:"limit,omitempty" desc:"Maximum number of lines to return (default 2000)"`
}

const readDescription = `Read a text file. Output lines are numbered "<n>\t<line>" (the number is not part of the file).
Returns at most 2000 lines; use offset and limit to page through larger files. Lines longer than 2000 characters are clipped.
Read a file before editing it. Prefer one read of the whole file over many small reads.`

func Read() Tool {
	return New("read", readDescription, true, func(ctx context.Context, env *Env, a readArgs) Result {
		abs, err := env.Resolve(a.Path)
		if err != nil {
			return errorf("%v", err)
		}
		if denied := env.permit(ctx, "read", abs); denied != nil {
			return *denied
		}
		info, err := os.Stat(abs)
		if err != nil {
			return errorf("cannot read %s: %v", a.Path, err)
		}
		if info.IsDir() {
			return errorf("%s is a directory; use ls to list it", a.Path)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return errorf("cannot read %s: %v", a.Path, err)
		}
		if isBinary(data) {
			return errorf("%s looks like a binary file (%d bytes); read only works on text", a.Path, len(data))
		}
		env.Reads.MarkRead(abs, info.ModTime())
		return Result{Content: numberLines(string(data), a.Offset, a.Limit), Details: map[string]any{"path": abs}}
	})
}

func isBinary(b []byte) bool {
	head := b[:min(len(b), 8000)]
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(trimPartialRune(head))
}

func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}

func numberLines(s string, offset, limit int) string {
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 || limit > MaxLines {
		limit = MaxLines
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if offset > total {
		return fmt.Sprintf("(File has %d lines; offset %d is past the end.)", total, offset)
	}
	end := min(offset-1+limit, total)
	var b strings.Builder
	used := 0
	for i := offset - 1; i < end; i++ {
		line := strings.TrimRight(lines[i], "\r\n")
		if utf8.RuneCountInString(line) > MaxLineChars {
			line = string([]rune(line)[:MaxLineChars]) + "… (line truncated to 2000 chars)"
		}
		row := fmt.Sprintf("%6d\t%s\n", i+1, line)
		if used+len(row) > MaxBytes {
			end = i
			break
		}
		b.WriteString(row)
		used += len(row)
	}
	if end < total || offset > 1 {
		fmt.Fprintf(&b, "\n(Showing lines %d-%d of %d.", offset, end, total)
		if end < total {
			fmt.Fprintf(&b, " Use offset=%d to continue.", end+1)
		}
		b.WriteString(")\n")
	}
	return b.String()
}
