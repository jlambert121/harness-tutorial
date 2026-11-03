package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"harness/internal/llm"
	"harness/internal/permission"
)

// Env is what tools may touch. It grows in later lessons (permissions, read tracking).
type Env struct {
	Root        string       // workspace root; file tools are confined to it
	OverflowDir string       // where oversized outputs are saved; default $TMPDIR/harness-overflow
	Reads       *ReadTracker // nil disables read-before-edit checks
	Cwd         string       // bash working directory; "" means Root

	Perm    *permission.Checker // nil allows everything
	Sandbox *permission.Sandbox // nil runs bash unsandboxed
}

// permit checks a file-tool call against the permission checker. It returns
// nil when the call may proceed, or the error result to return to the model.
func (e *Env) permit(ctx context.Context, tool string, abs string) *Result {
	if e.Perm == nil {
		return nil
	}
	if ok, note := e.Perm.Check(ctx, permission.Request{Tool: tool, Subjects: []string{relPath(e, abs)}}); !ok {
		return &Result{IsError: true, Content: "Permission denied: " + note}
	}
	return nil
}

// Result separates what the model sees (Content) from what a UI may render (Details).
type Result struct {
	Content string
	Details any
	IsError bool
}

type Tool interface {
	Spec() llm.ToolSpec
	ReadOnly() bool
	Run(ctx context.Context, env *Env, args json.RawMessage) Result
}

// Func adapts a function to Tool.
type Func struct {
	Def llm.ToolSpec
	RO  bool
	Fn  func(ctx context.Context, env *Env, args json.RawMessage) Result
}

func (f Func) Spec() llm.ToolSpec { return f.Def }
func (f Func) ReadOnly() bool     { return f.RO }
func (f Func) Run(ctx context.Context, env *Env, args json.RawMessage) Result {
	return f.Fn(ctx, env, args)
}

type Registry struct {
	env   *Env
	tools map[string]Tool
	// Parallel lets consecutive read-only calls run concurrently (default true).
	Parallel bool
}

func NewRegistry(env *Env) *Registry {
	return &Registry{env: env, tools: map[string]Tool{}, Parallel: true}
}

func (r *Registry) Env() *Env { return r.env }

func (r *Registry) Register(t Tool) { r.tools[t.Spec().Name] = t }

func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.tools[name]; return t, ok }

// Specs returns tool definitions sorted by name, so the request prefix is
// identical on every call regardless of registration order.
func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec())
	}
	slices.SortFunc(specs, func(a, b llm.ToolSpec) int { return strings.Compare(a.Name, b.Name) })
	return specs
}

func (r *Registry) names() []string {
	var n []string
	for _, s := range r.Specs() {
		n = append(n, s.Name)
	}
	return n
}

// Execute runs calls and returns one result per call, in call order.
// Consecutive read-only calls run concurrently; any other call waits for the
// calls before it and blocks the calls after it. Once ctx is cancelled,
// calls not yet started get an "aborted" result. Callbacks are serialized.
func (r *Registry) Execute(ctx context.Context, calls []llm.ToolCall, onStart func(llm.ToolCall), onEnd func(llm.ToolCall, llm.ToolResult)) []llm.ToolResult {
	results := make([]llm.ToolResult, len(calls))
	var mu sync.Mutex
	finish := func(i int, res Result) {
		results[i] = llm.ToolResult{CallID: calls[i].ID, Name: calls[i].Name, Content: res.Content, IsError: res.IsError}
		if onEnd != nil {
			mu.Lock()
			onEnd(calls[i], results[i])
			mu.Unlock()
		}
	}
	for i := 0; i < len(calls); {
		j := i + 1
		if r.Parallel && r.readOnly(calls[i]) {
			for j < len(calls) && r.readOnly(calls[j]) {
				j++
			}
		}
		if ctx.Err() != nil {
			for k := i; k < len(calls); k++ {
				finish(k, Result{Content: "Tool execution aborted: the user cancelled before this call ran.", IsError: true})
			}
			break
		}
		// onStart fires in call order for the whole batch; onEnd fires as each call completes.
		for k := i; k < j; k++ {
			if onStart != nil {
				onStart(calls[k])
			}
		}
		var wg sync.WaitGroup
		for k := i; k < j; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				finish(k, r.run(ctx, calls[k]))
			}()
		}
		wg.Wait()
		i = j
	}
	return results
}

func (r *Registry) readOnly(c llm.ToolCall) bool {
	t, ok := r.tools[c.Name]
	return !ok || t.ReadOnly()
}

func (r *Registry) run(ctx context.Context, c llm.ToolCall) Result {
	t, ok := r.tools[c.Name]
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("Unknown tool %q. Available tools: %s.", c.Name, strings.Join(r.names(), ", "))}
	}
	args := c.Args
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if r.env.Perm != nil {
		if ok, note := r.env.Perm.CheckRepeat(ctx, c.Name, string(args)); !ok {
			return Result{IsError: true, Content: note}
		}
	}
	return r.capOutput(t.Run(ctx, r.env, args))
}

// capOutput is the last line of defense against a tool flooding the context:
// oversized output is saved to a file and the model gets the head and tail
// plus the path, so it can grep or page through the rest.
func (r *Registry) capOutput(res Result) Result {
	out, cut := Truncate(res.Content, MaxLines, MaxBytes, TruncHeadTail)
	if !cut {
		return res
	}
	note := "The full output is not shown."
	if p, err := SaveOverflow(r.env, res.Content); err == nil {
		note = fmt.Sprintf("Full output saved to %s; use grep or read with offset/limit on that file instead of re-running.", p)
	}
	res.Content = out + "\n[" + note + "]\n"
	return res
}
