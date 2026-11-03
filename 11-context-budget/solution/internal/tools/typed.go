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

func relPath(env *Env, abs string) string {
	if rel, err := filepath.Rel(env.Root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return abs
}
