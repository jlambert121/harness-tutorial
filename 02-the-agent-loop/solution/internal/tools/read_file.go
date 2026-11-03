package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/llm"
)

const readFileLimit = 100 * 1024

func ReadFile() Tool {
	return Func{
		Def: llm.ToolSpec{
			Name:        "read_file",
			Description: "Read a UTF-8 text file from the workspace. Paths are relative to the workspace root.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the workspace root"}},"required":["path"],"additionalProperties":false}`),
		},
		RO: true,
		Fn: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
				return Result{IsError: true, Content: `invalid arguments: expected {"path": "<file>"}`}
			}
			abs, err := insideRoot(env.Root, args.Path)
			if err != nil {
				return Result{IsError: true, Content: err.Error()}
			}
			b, err := os.ReadFile(abs)
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("cannot read %s: %v", args.Path, err)}
			}
			if len(b) > readFileLimit {
				return Result{IsError: true, Content: fmt.Sprintf("%s is %d bytes; files over %d bytes are not supported yet", args.Path, len(b), readFileLimit)}
			}
			return Result{Content: string(b)}
		},
	}
}

// insideRoot resolves path against root lexically and rejects escapes.
// Lesson 04 replaces this with a symlink-aware check.
func insideRoot(root, path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, path)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the workspace (%s)", path, root)
	}
	return abs, nil
}
