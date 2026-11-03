package subagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/hooks"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/subagent"
	"harness/internal/tools"
)

type setup struct {
	parent *agent.Agent
	srv    *fake.Server
	events []agent.Event
}

func newSetup(t *testing.T, defs []subagent.Def, turns ...fake.Turn) *setup {
	t.Helper()
	srv := fake.Start(t, turns...)
	client := llm.NewGateway(srv.URL, "k", llm.WithRetry(0, 0, 0))
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	reg := tools.NewRegistry(&tools.Env{Root: root, Reads: tools.NewReadTracker()})
	for _, tl := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls(), tools.Edit(), tools.Write()} {
		reg.Register(tl)
	}
	reg.Register(subagent.TaskTool(subagent.Runner{Client: client, Parent: reg, DefaultModel: "parent-model", Defs: defs}))
	s := &setup{srv: srv}
	s.parent = agent.New(agent.Config{Client: client, Model: "parent-model", System: "parent system", Tools: reg})
	return s
}

func (s *setup) run(t *testing.T, input string) {
	t.Helper()
	if err := s.parent.Run(context.Background(), input, func(e agent.Event) { s.events = append(s.events, e) }); err != nil {
		t.Fatal(err)
	}
}

func body(t *testing.T, raw json.RawMessage) map[string]any {
	var b map[string]any
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

var explore = subagent.Def{Name: "explore", Description: "Read-only research", Model: "small-model",
	System: "You are a read-only research agent.", Tools: []string{"read", "grep", "glob", "ls", "task"}}

func TestSubagentIsolatedHistoryAndTools(t *testing.T) {
	s := newSetup(t, []subagent.Def{explore},
		fake.Tools(fake.Call("t1", "task", map[string]string{"agent": "explore", "description": "find X", "prompt": "Where is X defined?"})),
		fake.Text("X is in a.go:1"),
		fake.Text("Done: X is in a.go."),
	)
	s.run(t, "a long parent conversation message that the child must not see")
	reqs := s.srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d (parent, child, parent)", len(reqs))
	}
	child := body(t, reqs[1])
	if child["model"] != "small-model" {
		t.Errorf("child model = %v, want the definition's model", child["model"])
	}
	msgs := child["messages"].([]any)
	if len(msgs) != 2 || !strings.Contains(string(reqs[1]), "Where is X defined?") || strings.Contains(string(reqs[1]), "must not see") {
		t.Errorf("child must start with its own system prompt and the task prompt only: %s", reqs[1])
	}
	var names []string
	for _, tl := range child["tools"].([]any) {
		names = append(names, tl.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "glob,grep,ls,read" {
		t.Errorf("child tools = %v; the definition's list, and never task (no nesting)", names)
	}
	parentFinal := body(t, reqs[2])["messages"].([]any)
	result := parentFinal[len(parentFinal)-1].(map[string]any)
	if result["role"] != "tool" || !strings.Contains(result["content"].(string), "<task_result") || !strings.Contains(result["content"].(string), "X is in a.go:1") {
		t.Errorf("the parent receives only the child's final answer: %v", result)
	}
}

func TestUnknownAgent(t *testing.T) {
	s := newSetup(t, []subagent.Def{explore},
		fake.Tools(fake.Call("t1", "task", map[string]string{"agent": "nope", "description": "d", "prompt": "p"})),
		fake.Text("ok"),
	)
	s.run(t, "go")
	last := body(t, s.srv.Requests()[1])["messages"].([]any)
	if c := last[len(last)-1].(map[string]any)["content"].(string); !strings.Contains(c, "explore") {
		t.Errorf("unknown agent should list the available ones: %q", c)
	}
}

func TestParallelSubagents(t *testing.T) {
	slow := fake.Turn{Text: "child answer", Delay: 40 * time.Millisecond, ChunkSize: 2}
	s := newSetup(t, []subagent.Def{explore},
		fake.Tools(
			fake.Call("t1", "task", map[string]string{"agent": "explore", "description": "one", "prompt": "q1"}),
			fake.Call("t2", "task", map[string]string{"agent": "explore", "description": "two", "prompt": "q2"}),
		),
		slow, slow,
		fake.Text("both done"),
	)
	start := time.Now()
	s.run(t, "go")
	el := time.Since(start)
	single := 40 * time.Millisecond * 8 // ~8 chunks per child
	if el > single*3/2 {
		t.Errorf("two subagents took %v; they should run concurrently (one alone ≈ %v)", el, single)
	}
}

func TestChildUsageCountedInParent(t *testing.T) {
	s := newSetup(t, []subagent.Def{explore},
		fake.Turn{Calls: []llm.ToolCall{fake.Call("t1", "task", map[string]string{"agent": "explore", "description": "d", "prompt": "p"})}, Usage: llm.Usage{Input: 100, Output: 10, CostUSD: 0.001}},
		fake.Turn{Text: "child", Usage: llm.Usage{Input: 1000, Output: 50, CostUSD: 0.01}},
		fake.Turn{Text: "done", Usage: llm.Usage{Input: 200, Output: 5, CostUSD: 0.002}},
	)
	s.run(t, "go")
	end := s.events[len(s.events)-1]
	if end.Kind != agent.AgentEnd || end.Usage.Input != 1300 || diff(end.Usage.CostUSD, 0.013) > 1e-9 {
		t.Errorf("run usage = %+v; must include the subagent's tokens and cost", end.Usage)
	}
	var phases []string
	for _, e := range s.events {
		if e.Kind == agent.Subagent {
			phases = append(phases, e.Data["phase"].(string))
		}
	}
	if len(phases) < 2 || phases[0] != "start" || phases[len(phases)-1] != "end" {
		t.Errorf("subagent events = %v", phases)
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestLoadDefs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("---\nname: reviewer\ndescription: Reviews diffs\nmodel: anthropic/claude-haiku-5.5\ntools: read, grep\nmax_steps: 12\n---\nYou review code.\n"), 0o644)
	defs := subagent.LoadDefs(dir)
	var r *subagent.Def
	for i := range defs {
		if defs[i].Name == "reviewer" {
			r = &defs[i]
		}
	}
	if r == nil || r.Model != "anthropic/claude-haiku-5.5" || strings.Join(r.Tools, ",") != "read,grep" || r.MaxSteps != 12 || r.System != "You review code." {
		t.Errorf("defs = %+v", defs)
	}
	var builtin bool
	for _, d := range subagent.LoadDefs() {
		if d.Name == "explore" {
			builtin = true
		}
	}
	if !builtin {
		t.Error("the built-in explore agent should always be available")
	}
}

func TestSubagentToolsRunHooks(t *testing.T) {
	s := newSetup(t, []subagent.Def{explore},
		fake.Tools(fake.Call("t1", "task", map[string]string{"agent": "explore", "description": "d", "prompt": "read a.go"})),
		fake.Tools(fake.Call("r1", "read", map[string]string{"path": "a.go"})),
		fake.Text("child done"),
		fake.Text("parent done"),
	)
	env := s.parent.Tools().Env()
	env.Hooks = hooks.New(env.Root, []hooks.Hook{{Event: hooks.PreToolUse, Matcher: "read", Command: `echo "no reading in this test" >&2; exit 2`}})
	s.run(t, "go")
	childFollowUp := string(s.srv.Requests()[2])
	if !strings.Contains(childFollowUp, "no reading in this test") {
		t.Error("a subagent's tool calls must go through the same hooks as the parent's")
	}
}
