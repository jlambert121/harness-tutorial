// Package subagent runs child agents with their own context window, a
// restricted tool set, and optionally a different model, and returns only
// their final answer to the parent.
package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/tools"
)

type Def struct {
	Name, Description string
	Model             string   // "" = the parent's model
	System            string   // the child's whole system prompt
	Tools             []string // allowed tool names; "task" is always removed
	MaxSteps          int
}

// Explore is built in: read-only research on the small model, Claude Code's
// Explore and opencode's explore agent in spirit.
var Explore = Def{
	Name:        "explore",
	Description: "Read-only codebase research: finds files, definitions, and call paths, and reports them with file:line references.",
	System: `You are a read-only research agent working for another agent. Use read, grep, glob, and ls to answer the question you are given.
Search broadly first, then read only what you need. Request independent searches in the same response.
Your final message is all the other agent will see: answer the question directly in at most 300 words, with file:line references. Do not describe your search process.`,
	Tools:    []string{"read", "grep", "glob", "ls"},
	MaxSteps: 25,
}

// LoadDefs returns the built-in definitions plus *.md files from dirs
// (frontmatter: name, description, model, tools, max_steps; body: system prompt).
func LoadDefs(dirs ...string) []Def {
	byName := map[string]Def{Explore.Name: Explore}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			meta, body := frontmatter(string(b))
			if meta["name"] == "" || meta["description"] == "" {
				continue
			}
			def := Def{Name: meta["name"], Description: meta["description"], Model: meta["model"], System: body}
			for _, t := range strings.Split(meta["tools"], ",") {
				if t = strings.TrimSpace(t); t != "" {
					def.Tools = append(def.Tools, t)
				}
			}
			def.MaxSteps, _ = strconv.Atoi(meta["max_steps"])
			byName[def.Name] = def
		}
	}
	var out []Def
	for _, d := range byName {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Def) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func frontmatter(s string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(s, "---\n") {
		return meta, strings.TrimSpace(s)
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return meta, strings.TrimSpace(s)
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return meta, strings.TrimSpace(s[4+end+4:])
}

// Runner holds what a child agent is built from.
type Runner struct {
	Client       llm.Client
	Parent       *tools.Registry // tools are taken from here and filtered
	DefaultModel string
	Defs         []Def
	Cache        llm.CacheMode
}

type taskArgs struct {
	Agent       string `json:"agent" desc:"Which agent to run (see the tool description)"`
	Description string `json:"description" desc:"3-7 word summary of the task, shown to the user"`
	Prompt      string `json:"prompt" desc:"Complete, self-contained instructions: the agent sees nothing else from this conversation"`
}

// TaskTool launches a child agent. It is read-only from the registry's point
// of view so that several tasks in one response run in parallel; the child
// itself can only do what its tool list allows.
func TaskTool(r Runner) tools.Tool {
	var desc strings.Builder
	desc.WriteString("Delegate a self-contained task to another agent with its own context window. It returns only its final answer, which keeps intermediate searching out of your context. Launch independent tasks in the same response to run them in parallel. Agents:\n")
	for _, d := range r.Defs {
		fmt.Fprintf(&desc, "- %s: %s\n", d.Name, d.Description)
	}
	return tools.New("task", desc.String(), true, func(ctx context.Context, env *tools.Env, a taskArgs) tools.Result {
		var def *Def
		var names []string
		for i := range r.Defs {
			names = append(names, r.Defs[i].Name)
			if r.Defs[i].Name == a.Agent {
				def = &r.Defs[i]
			}
		}
		if def == nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("Unknown agent %q. Available: %s.", a.Agent, strings.Join(names, ", "))}
		}
		return r.run(ctx, env, *def, a)
	})
}

func (r Runner) run(ctx context.Context, parentEnv *tools.Env, def Def, a taskArgs) tools.Result {
	childEnv := &tools.Env{Root: parentEnv.Root, OverflowDir: parentEnv.OverflowDir, Reads: tools.NewReadTracker(),
		Cwd: parentEnv.Cwd, Perm: parentEnv.Perm, Sandbox: parentEnv.Sandbox}
	reg := tools.NewRegistry(childEnv)
	for _, name := range def.Tools {
		if name == "task" {
			continue
		}
		if t, ok := r.Parent.Get(name); ok {
			reg.Register(t)
		}
	}
	model := def.Model
	if model == "" {
		model = r.DefaultModel
	}
	maxSteps := def.MaxSteps
	if maxSteps == 0 {
		maxSteps = 30
	}
	child := agent.New(agent.Config{Client: r.Client, Model: model, System: def.System, Tools: reg, MaxSteps: maxSteps, Cache: r.Cache,
		Context: fmt.Sprintf("<environment>\nworking directory: %s\n</environment>", parentEnv.Root)})

	rc, _ := agent.RunContextFrom(ctx)
	id := newID()
	emit := func(phase string, data map[string]any) {
		if rc.Emit == nil {
			return
		}
		if data == nil {
			data = map[string]any{}
		}
		data["id"], data["agent"], data["description"], data["phase"] = id, def.Name, a.Description, phase
		rc.Emit(agent.Event{Kind: agent.Subagent, Data: data, Time: time.Now()})
	}
	emit("start", map[string]any{"model": model})
	var usage llm.Usage
	err := child.Run(ctx, a.Prompt, func(e agent.Event) {
		switch e.Kind {
		case agent.ToolStart:
			emit("tool", map[string]any{"tool": e.Call.Name, "args": string(e.Call.Args)})
		case agent.AgentEnd:
			if e.Usage != nil {
				usage = *e.Usage
			}
		}
	})
	if rc.AddUsage != nil {
		rc.AddUsage(usage)
	}
	emit("end", map[string]any{"input": usage.Input, "output": usage.Output, "cost_usd": usage.CostUSD})

	hist := child.History()
	answer := ""
	if n := len(hist); n > 0 && hist[n-1].Role == llm.RoleAssistant {
		answer = strings.TrimSpace(hist[n-1].Text())
	}
	if err != nil && answer == "" {
		return tools.Result{IsError: true, Content: fmt.Sprintf("<task_result agent=%q error=true>\nThe %s agent failed: %v\n</task_result>", def.Name, def.Name, err)}
	}
	return tools.Result{Content: fmt.Sprintf("<task_result agent=%q>\n%s\n</task_result>", def.Name, answer)}
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
