package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/diff"
)

type editArgs struct {
	Path       string `json:"path" desc:"File to edit"`
	OldString  string `json:"old_string" desc:"Exact text to replace, copied from the file, including indentation. Must match exactly one location unless replace_all is set."`
	NewString  string `json:"new_string" desc:"Replacement text"`
	ReplaceAll bool   `json:"replace_all,omitempty" desc:"Replace every occurrence of old_string"`
}

const editDescription = `Replace text in a file. old_string must appear exactly once; include enough surrounding lines to make it unique.
You must read the file before editing it. Do not include the line-number prefix from read output in old_string.
To create a new file, use write.`

func Edit() Tool {
	return New("edit", editDescription, false, func(ctx context.Context, env *Env, a editArgs) Result {
		if a.OldString == "" {
			return errorf("old_string is empty. To create a file or replace its whole content, use write.")
		}
		if a.OldString == a.NewString {
			return errorf("old_string and new_string are identical; nothing to change.")
		}
		abs, err := env.Resolve(a.Path)
		if err != nil {
			return errorf("%v", err)
		}
		unlock := env.Reads.Lock(abs)
		defer unlock()
		if err := env.Reads.Check(abs); err != nil {
			return errorf("%v", err)
		}
		if denied := env.permit(ctx, "edit", abs); denied != nil {
			return *denied
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return errorf("cannot read %s: %v", a.Path, err)
		}
		f := decodeText(string(raw))
		old, repl := toLF(a.OldString), toLF(a.NewString)

		var updated string
		var how string
		if a.ReplaceAll {
			n := strings.Count(f.body, old)
			if n == 0 {
				return errorf("Could not find old_string in %s (replace_all uses exact matching). Read the file again and copy the text exactly.", a.Path)
			}
			updated = strings.ReplaceAll(f.body, old, repl)
			how = fmt.Sprintf("replaced %d occurrences", n)
		} else {
			start, end, used, err := FindMatch(f.body, old, AllStrategies)
			var amb *AmbiguousError
			switch {
			case errors.As(err, &amb):
				return errorf("Found %d matches for old_string in %s. Provide more surrounding context to make it unique, or set replace_all to change every occurrence.", amb.Count, a.Path)
			case err != nil:
				return errorf("Could not find old_string in %s. Read the file again and copy the text exactly, including whitespace; do not include line-number prefixes.", a.Path)
			}
			updated = f.body[:start] + repl + f.body[end:]
			how = "strategy " + string(used)
		}
		if err := writeKeepMode(abs, f.encode(updated)); err != nil {
			return errorf("cannot write %s: %v", a.Path, err)
		}
		markRead(env, abs)
		d := diff.Unified(f.body, updated, relPath(env, abs), 3)
		return Result{Content: fmt.Sprintf("Edited %s (%s).\n%s", relPath(env, abs), how, d), Details: map[string]any{"diff": d}}
	})
}

type writeArgs struct {
	Path    string `json:"path" desc:"File to create or overwrite"`
	Content string `json:"content" desc:"Complete new file content"`
}

func Write() Tool {
	return New("write", "Create a file, or replace a file's entire content. Parent directories are created. Overwriting an existing file requires reading it first. Prefer edit for changes to existing files.", false,
		func(ctx context.Context, env *Env, a writeArgs) Result {
			abs, err := env.Resolve(a.Path)
			if err != nil {
				return errorf("%v", err)
			}
			unlock := env.Reads.Lock(abs)
			defer unlock()
			verb := "Created"
			if _, err := os.Stat(abs); err == nil {
				if err := env.Reads.Check(abs); err != nil {
					return errorf("%v", err)
				}
				verb = "Overwrote"
			}
			if denied := env.permit(ctx, "write", abs); denied != nil {
				return *denied
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return errorf("%v", err)
			}
			if err := writeKeepMode(abs, a.Content); err != nil {
				return errorf("cannot write %s: %v", a.Path, err)
			}
			markRead(env, abs)
			return Result{Content: fmt.Sprintf("%s %s (%d bytes, %d lines).", verb, relPath(env, abs), len(a.Content), strings.Count(a.Content, "\n"))}
		})
}

func markRead(env *Env, abs string) {
	if fi, err := os.Stat(abs); err == nil {
		env.Reads.MarkRead(abs, fi.ModTime())
	}
}

func writeKeepMode(path, content string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, []byte(content), mode)
}

// textFile is file content normalized to LF without a BOM, remembering how to restore it.
type textFile struct {
	body string
	bom  bool
	crlf bool
}

func decodeText(s string) textFile {
	f := textFile{}
	if strings.HasPrefix(s, "\uFEFF") {
		f.bom, s = true, strings.TrimPrefix(s, "\uFEFF")
	}
	if strings.Contains(s, "\r\n") {
		f.crlf = true
	}
	f.body = toLF(s)
	return f
}

func (f textFile) encode(body string) string {
	if f.crlf {
		body = strings.ReplaceAll(body, "\n", "\r\n")
	}
	if f.bom {
		body = "\uFEFF" + body
	}
	return body
}

func toLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
