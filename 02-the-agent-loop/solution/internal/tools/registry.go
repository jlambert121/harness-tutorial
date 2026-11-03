package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"harness/internal/llm"
)

// Env is what tools may touch. It grows in later lessons (permissions, read tracking).
type Env struct {
	Root string // workspace root; file tools are confined to it
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
}

func NewRegistry(env *Env) *Registry { return &Registry{env: env, tools: map[string]Tool{}} }

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

// Execute runs calls in order and returns one result per call, in call order.
// Once ctx is cancelled, remaining calls are not started and get an "aborted" result.
func (r *Registry) Execute(ctx context.Context, calls []llm.ToolCall, onStart func(llm.ToolCall), onEnd func(llm.ToolCall, llm.ToolResult)) []llm.ToolResult {
	results := make([]llm.ToolResult, len(calls))
	for i, c := range calls {
		var res Result
		if ctx.Err() != nil {
			res = Result{Content: "Tool execution aborted: the user cancelled before this call ran.", IsError: true}
		} else {
			if onStart != nil {
				onStart(c)
			}
			res = r.run(ctx, c)
		}
		results[i] = llm.ToolResult{CallID: c.ID, Name: c.Name, Content: res.Content, IsError: res.IsError}
		if onEnd != nil {
			onEnd(c, results[i])
		}
	}
	return results
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
	return t.Run(ctx, r.env, args)
}
