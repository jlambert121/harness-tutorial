// Command harness runs the coding agent.
//
//	harness [flags]                 interactive REPL
//	harness exec [flags] "prompt"   one headless run; --json prints events as JSONL
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/agent"
	"harness/internal/commands"
	"harness/internal/ctxmgr"
	"harness/internal/hooks"
	"harness/internal/llm"
	"harness/internal/mcp"
	"harness/internal/memory"
	"harness/internal/permission"
	"harness/internal/prompt"
	"harness/internal/session"
	"harness/internal/subagent"
	"harness/internal/tools"
	"harness/internal/trace"
	"harness/internal/ui/repl"
	"harness/internal/ui/tui"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type options struct {
	model, base, root, tracePath string
	maxSteps                     int
	yes, json                    bool
	ui, mode                     string
	sandbox, network             bool
	rules                        multi
	resume                       string
	cont, noSession              bool
	window                       int
	cache                        string
	hostile, noCompact, noMemory bool
	noSubagents                  bool
	smallModel, hooksFile        string
	mcpDeferred                  bool
}

func flags(name string, args []string) (*options, *flag.FlagSet, error) {
	o := &options{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&o.model, "model", env("HARNESS_MODEL", "anthropic/claude-sonnet-5.5"), "gateway model id")
	fs.StringVar(&o.base, "base", env("HARNESS_BASE_URL", "https://ai-gateway.vercel.sh/v1"), "gateway base URL")
	fs.StringVar(&o.root, "root", ".", "workspace root")
	fs.IntVar(&o.maxSteps, "max-steps", 50, "max model calls per prompt (0 = unlimited)")
	fs.StringVar(&o.tracePath, "trace", "", "write events as JSONL to this file")
	fs.BoolVar(&o.yes, "yes", false, "same as --mode yolo")
	fs.StringVar(&o.mode, "mode", "", "permission mode: default | accept-edits | plan | yolo (exec defaults to yolo)")
	fs.Var(&o.rules, "rule", `extra permission rule, e.g. --rule "allow bash(go test*)" (repeatable)`)
	fs.BoolVar(&o.sandbox, "sandbox", permission.SandboxAvailable(), "run bash inside the OS sandbox (macOS)")
	fs.BoolVar(&o.network, "network", false, "allow network access inside the sandbox")
	fs.BoolVar(&o.cont, "continue", false, "resume the most recent session for this workspace")
	fs.StringVar(&o.resume, "resume", "", "resume a session by file path or ID")
	fs.BoolVar(&o.noSession, "no-session", false, "do not save this session")
	fs.IntVar(&o.window, "context-window", 0, "override the model's context window (tokens), e.g. to test pruning")
	fs.StringVar(&o.cache, "cache", "manual", "prompt caching: manual | auto | off")
	fs.BoolVar(&o.hostile, "cache-hostile", false, "demo only: put the time in the system prompt and shuffle tools on every request")
	fs.BoolVar(&o.noCompact, "no-compact", false, "disable compaction")
	fs.BoolVar(&o.noMemory, "no-memory", false, "disable cross-session memory")
	fs.BoolVar(&o.noSubagents, "no-subagents", false, "do not offer the task tool")
	fs.BoolVar(&o.mcpDeferred, "mcp-deferred", false, "load MCP tool definitions on demand through tool_search")
	fs.StringVar(&o.hooksFile, "hooks", "", "extra hooks.json (in addition to ~/.config/harness/hooks.json and <root>/.harness/hooks.json)")
	fs.StringVar(&o.smallModel, "small-model", env("HARNESS_SMALL_MODEL", "anthropic/claude-haiku-5.5"), "model for the explore subagent and other background work")
	fs.BoolVar(&o.json, "json", false, "exec: print events as JSONL on stdout")
	fs.StringVar(&o.ui, "ui", "", "tui | repl (default: tui when stdout is a terminal)")
	err := fs.Parse(args)
	return o, fs, err
}

func dataDir() string {
	if d := os.Getenv("HARNESS_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "harness")
}

// app is everything main needs besides the agent.
type app struct {
	o        *options
	root     string
	skills   []commands.Skill
	mem      *memory.Store
	submit   func(ctx context.Context, text string) error
	store    *session.Store
	snaps    *session.Snapshots
	ctx      *ctxmgr.Manager
	observer agent.Sink // must receive every event (calibrates token estimates)
}

// modelInfo looks the model up in the gateway catalog; unknown models get a
// conservative 128k window.
func modelInfo(o *options) llm.ModelInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if models, err := llm.FetchCatalog(ctx, o.base, os.Getenv("AI_GATEWAY_API_KEY")); err == nil {
		for _, m := range models {
			if m.ID == o.model {
				return m
			}
		}
	}
	return llm.ModelInfo{ID: o.model, ContextWindow: 128000, MaxTokens: 16000}
}

// openSession creates, continues, or resumes the session file for root.
func openSession(o *options, root string) (*session.Store, error) {
	dir := session.DirFor(dataDir(), root)
	switch {
	case o.resume != "":
		path := o.resume
		if _, err := os.Stat(path); err != nil {
			list, _ := session.List(dir)
			path = ""
			for _, s := range list {
				if s.ID == o.resume {
					path = s.Path
				}
			}
			if path == "" {
				return nil, fmt.Errorf("no session %q in %s", o.resume, dir)
			}
		}
		return session.Open(path)
	case o.cont:
		path, err := session.Latest(dir)
		if err != nil {
			return nil, err
		}
		return session.Open(path)
	}
	return session.Create(dir, root)
}

func build(o *options) (*agent.Agent, []prompt.Instruction, error) {
	a, ins, _, err := buildApp(o)
	return a, ins, err
}

func buildApp(o *options) (*agent.Agent, []prompt.Instruction, *app, error) {
	absRoot, err := filepath.Abs(o.root)
	if err != nil {
		return nil, nil, nil, err
	}
	reg := tools.NewRegistry(&tools.Env{Root: absRoot, Reads: tools.NewReadTracker()})
	for _, t := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls(), tools.Edit(), tools.Write(), tools.Bash()} {
		reg.Register(t)
	}
	home, _ := os.UserHomeDir()
	mode := permission.Mode(o.mode)
	if o.yes {
		mode = permission.ModeYolo
	}
	if mode == "" {
		mode = permission.ModeDefault
	}
	rules, err := permission.LoadRules(filepath.Join(home, ".config", "harness", "permissions.txt"), filepath.Join(absRoot, ".harness", "permissions.txt"))
	if err != nil {
		return nil, nil, nil, err
	}
	for _, s := range o.rules {
		r, err := permission.ParseRule(s)
		if err != nil {
			return nil, nil, nil, err
		}
		rules = append(rules, r)
	}
	reg.Env().Perm = permission.NewChecker(mode, append(permission.DefaultRules(), rules...), nil)
	if o.sandbox {
		reg.Env().Sandbox = &permission.Sandbox{Writable: writableRoots(absRoot), Protected: []string{filepath.Join(absRoot, ".git")}, Network: o.network}
	}
	instructions := prompt.Discover(absRoot, home, 32*1024)
	var client llm.Client = llm.NewGateway(o.base, os.Getenv("AI_GATEWAY_API_KEY"))
	if o.hostile {
		client = hostileClient{client}
	}
	if !o.noSubagents {
		defs := subagent.LoadDefs(filepath.Join(home, ".config", "harness", "agents"), filepath.Join(absRoot, ".harness", "agents"))
		for i := range defs {
			if defs[i].Name == subagent.Explore.Name && defs[i].Model == "" {
				defs[i].Model = o.smallModel
			}
		}
		reg.Register(subagent.TaskTool(subagent.Runner{Client: client, Parent: reg, DefaultModel: o.model, Defs: defs, Cache: llm.CacheMode(o.cache)}))
	}
	if err := startMCP(reg, o, filepath.Join(home, ".config", "harness", "mcp.json"), filepath.Join(absRoot, ".harness", "mcp.json")); err != nil {
		return nil, nil, nil, err
	}
	hookFiles := []string{filepath.Join(home, ".config", "harness", "hooks.json"), filepath.Join(absRoot, ".harness", "hooks.json")}
	if o.hooksFile != "" {
		hookFiles = append(hookFiles, o.hooksFile)
	}
	hs, err := hooks.Load(hookFiles...)
	if err != nil {
		return nil, nil, nil, err
	}
	var hookRunner *hooks.Runner
	if len(hs) > 0 {
		hookRunner = hooks.New(absRoot, hs)
		reg.Env().Hooks = hookRunner
	}
	var mem *memory.Store
	if !o.noMemory {
		mem = memory.Open(filepath.Join(dataDir(), "memory", filepath.Base(session.DirFor("", absRoot))))
		reg.Register(mem.Tool())
	}
	skills := commands.LoadSkills(filepath.Join(home, ".config", "harness", "skills"), filepath.Join(absRoot, ".harness", "skills"))
	if len(skills) > 0 {
		reg.Register(commands.SkillTool(skills))
	}
	p := prompt.Build(o.model, prompt.DetectEnv(absRoot), instructions, reg.Specs())
	if l := commands.SkillListing(skills); l != "" {
		p.Context += "\n\n" + l
	}
	if out := hookRunner.Run(context.Background(), hooks.SessionStart, "", nil); out.AdditionalContext != "" {
		p.Context += "\n\n<session_start_hooks>\n" + out.AdditionalContext + "\n</session_start_hooks>"
	}
	if mem != nil {
		p.Context += "\n\n" + memory.Guidance
		if r := mem.Recall(200, 25*1024); r != "" {
			p.Context += "\n\n" + r
		}
	}
	cfg := agent.Config{
		Client: client, Cache: llm.CacheMode(o.cache),
		Model: o.model, System: p.System, Context: p.Context, Tools: reg, MaxSteps: o.maxSteps,
	}
	ap := &app{o: o, root: absRoot, skills: skills, mem: mem}
	ap.ctx = ctxmgr.NewManager(ctxmgr.NewBudget(modelInfo(o), o.window), 0.7, 40000)
	ap.ctx.MinClear = 20000
	compactor := ctxmgr.NewCompactor(client, o.model, min(20000, ap.ctx.Budget.Usable()/3))
	compactor.Cache = cfg.Cache
	// lastAfter guards against thrashing: if the previous compaction left the
	// context nearly as full as before, compacting again cannot help.
	lastAfter := 0
	cfg.ShouldCompact = func(system string, tools []llm.ToolSpec, msgs []llm.Message) bool {
		est := ap.ctx.Cal.EstimateRequest(system, tools, msgs)
		return ap.ctx.Need(est) && (lastAfter == 0 || est > lastAfter*6/5)
	}
	cfg.Compact = func(ctx context.Context, system string, tools []llm.ToolSpec, msgs []llm.Message, focus string) ([]llm.Message, error) {
		res, err := compactor.Compact(ctx, system, tools, msgs, ctxmgr.CompactOptions{
			Focus: focus, Context: p.Context, RecentFiles: reg.Env().Reads.Recent(5)})
		if err != nil {
			return nil, err
		}
		ap.ctx.Reset()
		lastAfter = ap.ctx.Cal.EstimateRequest(system, tools, res.History)
		return res.History, nil
	}
	cfg.IsOverflow = ctxmgr.IsOverflow
	if hookRunner != nil {
		cfg.OnPrompt = func(ctx context.Context, input string) (bool, string, string) {
			out := hookRunner.Run(ctx, hooks.UserPromptSubmit, "", map[string]any{"prompt": input})
			return out.Block, out.Reason, out.AdditionalContext
		}
		cfg.OnStop = func(ctx context.Context) string {
			out := hookRunner.Run(ctx, hooks.Stop, "", nil)
			if out.Block {
				return out.Reason
			}
			return ""
		}
	}
	if o.noCompact {
		cfg.ShouldCompact, cfg.Compact = nil, nil
	}
	if o.window > 0 {
		cfg.Client = windowGuard{client, ap.ctx}
		compactor.Client = client
	}
	cfg.Transform = ap.ctx.Transform
	ap.observer = func(e agent.Event) {
		if e.Kind == agent.MessageEnd && e.Usage != nil {
			ap.ctx.Observe(*e.Usage)
		}
	}
	var resumed []llm.Message
	if !o.noSession {
		store, err := openSession(o, absRoot)
		if err != nil {
			return nil, nil, nil, err
		}
		ap.store = store
		resumed = store.Messages(store.Leaf())
		cfg.SessionID = store.ID()
		cfg.Persist = func(m llm.Message) { store.AppendMessage(m) }
		cfg.OnCompact = func(h []llm.Message) { store.AppendCompaction(h) }
		if snaps, err := session.NewSnapshots(dataDir(), absRoot); err == nil {
			ap.snaps = snaps
			cfg.OnRunStart = func(ctx context.Context, input string) {
				if tree, err := snaps.Track(ctx); err == nil {
					data, _ := json.Marshal(map[string]string{"tree": tree})
					store.Append(session.Entry{Type: "snapshot", Data: data})
				}
			}
		}
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("%x", rand.Int64())
	}
	a := agent.New(cfg)
	if len(resumed) > 0 {
		repaired := session.Repair(resumed)
		for _, m := range repaired[len(resumed):] {
			ap.store.AppendMessage(m)
		}
		a.Load(repaired)
	}
	return a, instructions, ap, nil
}

// writableRoots is where sandboxed commands may write: the workspace, the
// temp directory, and Go's build cache (so go build and go test work).
func writableRoots(root string) []string {
	roots := []string{root, os.TempDir()}
	if out, err := exec.Command("go", "env", "GOCACHE").Output(); err == nil {
		if c := strings.TrimSpace(string(out)); c != "" {
			roots = append(roots, c)
		}
	}
	return roots
}

func traceSink(path string) (agent.Sink, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return trace.NewRecorder(f), func() { f.Close() }, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		os.Exit(runExec(os.Args[2:], os.Stdout, os.Stderr))
	}
	o, _, err := flags("harness", os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	a, instructions, ap, err := buildApp(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	extra, closeTrace, err := traceSink(o.tracePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer closeTrace()
	extra = agent.Tee(extra, ap.observer)
	if o.ui == "" {
		o.ui = "repl"
		if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			o.ui = "tui"
		}
	}
	reg := ap.commands(a)
	if o.ui == "tui" {
		m := tui.New(a, o.model)
		m.SetCommands(reg)
		ap.submit = func(_ context.Context, text string) error { m.Submit(text); return nil }
		if err := tui.RunWith(m, extra); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	fmt.Printf("model %s · workspace %s — Ctrl-C cancels, Ctrl-D exits\n", o.model, a.Tools().Env().Root)
	for _, in := range instructions {
		fmt.Printf("loaded %s\n", in.Path)
	}
	if ap.store != nil {
		fmt.Printf("session %s (%d messages)\n", ap.store.Path(), len(a.History()))
	}
	opts := repl.Options{Extra: extra, Commands: reg, SetSubmit: func(f func(context.Context, string) error) { ap.submit = f }}
	if err := repl.Run(context.Background(), a, os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// commands builds the slash-command registry: built-ins, prompt templates
// from ~/.config/harness/commands and <root>/.harness/commands, and one
// /skill:<name> command per skill.
func (ap *app) commands(a *agent.Agent) *commands.Registry {
	reg := commands.NewRegistry()
	add := func(name, desc string, run func(ctx context.Context, args string) (string, error)) {
		reg.Register(commands.Command{Name: name, Description: desc, Run: run})
	}
	add("help", "List commands", func(context.Context, string) (string, error) {
		var b strings.Builder
		for _, c := range reg.List() {
			fmt.Fprintf(&b, "  /%-18s %s\n", c.Name+" "+c.ArgumentHint, c.Description)
		}
		return b.String(), nil
	})
	add("undo", "Restore files and conversation to before the last prompt", func(ctx context.Context, _ string) (string, error) { return ap.undo(ctx, a) })
	add("context", "Show what the context window holds", func(context.Context, string) (string, error) { return ap.contextReport(a), nil })
	add("compact", "Summarize older history now (optional focus)", func(ctx context.Context, focus string) (string, error) {
		before := len(a.History())
		if err := a.Compact(ctx, focus, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("compacted %d messages into %d", before, len(a.History())), nil
	})
	add("model", "Show or switch the model", func(_ context.Context, id string) (string, error) {
		if id == "" {
			return "model: " + a.Model(), nil
		}
		old := a.Model()
		a.SetModel(id)
		if ap.store != nil {
			data, _ := json.Marshal(map[string]string{"from": old, "to": id})
			ap.store.Append(session.Entry{Type: "model_change", Data: data})
		}
		return fmt.Sprintf("switched %s → %s (the new model's prompt cache starts cold)", old, id), nil
	})
	add("mode", "Show or set the permission mode: default | accept-edits | plan | yolo", func(_ context.Context, m string) (string, error) {
		perm := a.Tools().Env().Perm
		if m == "" {
			return "mode: " + string(perm.Mode()), nil
		}
		switch permission.Mode(m) {
		case permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo:
		default:
			return "", fmt.Errorf("unknown mode %q", m)
		}
		perm.SetMode(permission.Mode(m))
		note := map[string]string{
			"plan":         "Plan mode is active: explore and plan, but do not edit files or run commands that change anything. Present the plan and wait for approval.",
			"default":      "Plan mode has ended: you may edit files and run commands again (subject to approval).",
			"accept-edits": "File edits are now approved automatically; commands still need approval.",
			"yolo":         "All tool calls are now approved automatically.",
		}[m]
		a.Remind("Permission mode changed to " + m + ". " + note)
		return "mode: " + m, nil
	})
	add("clear", "Start a new session with an empty context", func(context.Context, string) (string, error) {
		a.Load(nil)
		if ap.store != nil {
			ap.store.Close()
			s, err := session.Create(session.DirFor(dataDir(), ap.root), ap.root)
			if err != nil {
				return "", err
			}
			ap.store = s
		}
		return "context cleared; new session started", nil
	})
	add("memory", "Show saved memories for this project", func(context.Context, string) (string, error) {
		if ap.mem == nil {
			return "memory is disabled (--no-memory)", nil
		}
		b, err := os.ReadFile(filepath.Join(ap.mem.Dir(), "MEMORY.md"))
		if err != nil || len(b) == 0 {
			return "no memories yet (" + ap.mem.Dir() + ")", nil
		}
		return ap.mem.Dir() + "\n" + string(b), nil
	})
	add("skills", "List skills", func(context.Context, string) (string, error) {
		if len(ap.skills) == 0 {
			return "no skills (add them under .harness/skills/<name>/SKILL.md)", nil
		}
		return commands.SkillListing(ap.skills), nil
	})
	send := func(ctx context.Context, text string) error {
		if ap.submit == nil {
			return fmt.Errorf("no UI to run prompts")
		}
		return ap.submit(ctx, text)
	}
	shell := func(ctx context.Context, cmd string) (string, error) {
		env := a.Tools().Env()
		if ok, note := env.Perm.CheckBash(ctx, cmd); !ok {
			return "", errors.New(note)
		}
		args, _ := json.Marshal(map[string]string{"command": cmd})
		res := tools.Bash().Run(ctx, env, args)
		out := res.Content
		if i := strings.LastIndex(out, "[exit code:"); i >= 0 {
			out = out[:i]
		}
		if res.IsError {
			return "", fmt.Errorf("%s", strings.TrimSpace(res.Content))
		}
		return strings.TrimRight(out, "\n"), nil
	}
	home, _ := os.UserHomeDir()
	reg.AddTemplates(commands.LoadTemplates(filepath.Join(home, ".config", "harness", "commands"), filepath.Join(ap.root, ".harness", "commands")), send, shell)
	for _, sk := range ap.skills {
		reg.Register(commands.Command{Name: "skill:" + sk.Name, Description: sk.Description, Run: func(ctx context.Context, args string) (string, error) {
			return "", send(ctx, fmt.Sprintf("Load the %s skill with the skill tool and follow it.\n\n%s", sk.Name, args))
		}})
	}
	return reg
}

// startMCP starts the servers listed in each existing mcp.json
// ({"servers": {"name": {"command": ["prog", "arg"], "env": {"K": "V"}, "deferred": false}}})
// and registers their tools. Servers live until the harness exits.
func startMCP(reg *tools.Registry, o *options, paths ...string) error {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var cfg struct {
			Servers map[string]struct {
				Command  []string          `json:"command"`
				Env      map[string]string `json:"env"`
				Deferred bool              `json:"deferred"`
			} `json:"servers"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s: %v", p, err)
		}
		for name, s := range cfg.Servers {
			var env []string
			for k, v := range s.Env {
				env = append(env, k+"="+os.ExpandEnv(v))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			c, err := mcp.Start(ctx, name, s.Command, env)
			if err == nil {
				_, err = mcp.Register(ctx, reg, name, c, s.Deferred || o.mcpDeferred)
			}
			cancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "mcp %s: %v (skipped)\n", name, err)
			}
		}
	}
	return nil
}

// hostileClient breaks the cache on purpose, the two ways Claude Code's team
// lists as having broken theirs: a timestamp in the static prompt and a
// non-deterministic tool order.
type hostileClient struct{ inner llm.Client }

func (h hostileClient) Stream(ctx context.Context, req llm.Request) <-chan llm.StreamEvent {
	req.System = fmt.Sprintf("Current time: %s\n\n%s", time.Now().Format(time.RFC3339Nano), req.System)
	req.Tools = append([]llm.ToolSpec(nil), req.Tools...)
	rand.Shuffle(len(req.Tools), func(i, j int) { req.Tools[i], req.Tools[j] = req.Tools[j], req.Tools[i] })
	return h.inner.Stream(ctx, req)
}

// windowGuard makes --context-window real: a request estimated above the
// window fails the way a provider would, with a "prompt is too long" error.
type windowGuard struct {
	inner llm.Client
	m     *ctxmgr.Manager
}

func (g windowGuard) Stream(ctx context.Context, req llm.Request) <-chan llm.StreamEvent {
	if est := g.m.Cal.EstimateRequest(req.System, req.Tools, req.Messages); est > g.m.Budget.Window {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Message: &llm.Message{Role: llm.RoleAssistant, Model: req.Model, Stop: llm.StopError,
			Err: fmt.Sprintf("gateway: HTTP 400: prompt is too long: %d tokens > %d maximum (simulated by --context-window)", est, g.m.Budget.Window)}}
		close(ch)
		return ch
	}
	return g.inner.Stream(ctx, req)
}

// contextReport estimates what the next request would contain.
func (ap *app) contextReport(a *agent.Agent) string {
	cal := &ap.ctx.Cal
	var sys, tools, user, asst, results int
	tools = cal.EstimateRequest("", a.Tools().Specs(), nil)
	sys = cal.EstimateRequest(a.System(), nil, nil)
	for _, m := range a.History() {
		n := cal.EstimateRequest("", nil, []llm.Message{m})
		switch {
		case m.Role == llm.RoleUser:
			user += n
		case m.Role == llm.RoleTool:
			results += n
		default:
			asst += n
		}
	}
	total := sys + tools + user + asst + results
	usable := ap.ctx.Budget.Usable()
	var b strings.Builder
	fmt.Fprintf(&b, "context: ~%d of %d usable tokens (%d%%) · window %d · estimate ratio %.2f\n", total, usable, total*100/max(usable, 1), ap.ctx.Budget.Window, cal.Ratio())
	for _, row := range []struct {
		name string
		n    int
	}{{"system prompt", sys}, {"tool definitions", tools}, {"user messages", user}, {"assistant messages", asst}, {"tool results", results}} {
		fmt.Fprintf(&b, "  %-20s %7d  %s\n", row.name, row.n, strings.Repeat("█", row.n*40/max(total, 1)))
	}
	return b.String()
}

// undo restores the files to the snapshot taken before the last prompt and
// moves the conversation back to that point. The undone turn stays in the
// session file as an abandoned branch.
func (ap *app) undo(ctx context.Context, a *agent.Agent) (string, error) {
	if ap.store == nil || ap.snaps == nil {
		return "", fmt.Errorf("sessions are disabled")
	}
	e, ok := ap.store.Last("snapshot")
	if !ok {
		return "nothing to undo", nil
	}
	var data struct{ Tree string }
	json.Unmarshal(e.Data, &data)
	changed, err := ap.snaps.Diff(ctx, data.Tree)
	if err != nil {
		return "", err
	}
	if err := ap.snaps.Restore(ctx, data.Tree); err != nil {
		return "", err
	}
	ap.store.Branch(e.ParentID)
	a.Load(session.Repair(ap.store.Messages(ap.store.Leaf())))
	if len(changed) == 0 {
		return "undid the last prompt (no files had changed)", nil
	}
	return fmt.Sprintf("undid the last prompt; restored %d file(s): %s", len(changed), strings.Join(changed, ", ")), nil
}

// runExec performs one headless run. Bash commands are not confirmed: run it
// only in throwaway workspaces (the eval runner uses temp directories).
// Exit status: 0 the agent finished, 1 it failed, 2 bad usage.
func runExec(args []string, stdout, stderr io.Writer) int {
	o, fs, err := flags("harness exec", args)
	if err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, `usage: harness exec [flags] "prompt"`)
		return 2
	}
	if o.mode == "" {
		o.mode = string(permission.ModeYolo)
	}
	a, _, ap, err := buildApp(o)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	extra, closeTrace, err := traceSink(o.tracePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeTrace()

	var out agent.Sink
	if o.json {
		rec := trace.NewRecorder(stdout)
		out = func(e agent.Event) {
			if e.Kind != agent.TextDelta && e.Kind != agent.ThinkingDelta {
				rec(e)
			}
		}
	} else {
		out = (&repl.Renderer{Out: stdout}).Sink
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := a.Run(ctx, fs.Arg(0), agent.Tee(out, extra, ap.observer)); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
