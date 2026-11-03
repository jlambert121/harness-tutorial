package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"harness/internal/llm"
)

// New builds a Tool whose schema is derived from Args and whose arguments are
// validated with Decode before fn runs.
func New[Args any](name, description string, readOnly bool, fn func(ctx context.Context, env *Env, args Args) Result) Tool {
	return Func{
		Def: llm.ToolSpec{Name: name, Description: description, Schema: SchemaFor[Args]()},
		RO:  readOnly,
		Fn: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			args, err := Decode[Args](raw)
			if err != nil {
				return Result{IsError: true, Content: err.Error()}
			}
			return fn(ctx, env, args)
		},
	}
}

func errorf(format string, a ...any) Result {
	return Result{IsError: true, Content: fmt.Sprintf(format, a...)}
}

// insideRoot resolves path against the workspace root lexically and rejects
// escapes. The overflow directory is also readable so the model can page
// through saved output. Lesson 04 replaces this with a symlink-aware check.
func insideRoot(env *Env, path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(env.Root, path)
	}
	abs = filepath.Clean(abs)
	for _, dir := range []string{env.Root, env.overflowDir()} {
		rel, err := filepath.Rel(dir, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%s is outside the workspace (%s)", path, env.Root)
}

func relPath(env *Env, abs string) string {
	if rel, err := filepath.Rel(env.Root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return abs
}
