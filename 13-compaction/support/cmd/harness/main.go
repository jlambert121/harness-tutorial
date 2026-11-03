// Command harness runs the coding agent.
//
//	harness [flags]                 interactive REPL
//	harness exec [flags] "prompt"   one headless run; --json prints events as JSONL
package main

import (
	"context"
	"encoding/json"
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
	"harness/internal/ctxmgr"
	"harness/internal/llm"
	"harness/internal/permission"
	"harness/internal/prompt"
	"harness/internal/session"
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
	hostile, noCompact           bool
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
	p := prompt.Build(o.model, prompt.DetectEnv(absRoot), instructions, reg.Specs())
	var client llm.Client = llm.NewGateway(o.base, os.Getenv("AI_GATEWAY_API_KEY"))
	if o.hostile {
		client = hostileClient{client}
	}
	cfg := agent.Config{
		Client: client, Cache: llm.CacheMode(o.cache),
		Model: o.model, System: p.System, Context: p.Context, Tools: reg, MaxSteps: o.maxSteps,
	}
	ap := &app{}
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
	if o.ui == "tui" {
		if err := tui.Run(a, o.model, extra); err != nil {
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
	cmds := map[string]repl.Command{
		"undo":    func(ctx context.Context, _ string) (string, error) { return ap.undo(ctx, a) },
		"context": func(context.Context, string) (string, error) { return ap.contextReport(a), nil },
		"compact": func(ctx context.Context, focus string) (string, error) {
			before := len(a.History())
			if err := a.Compact(ctx, focus, nil); err != nil {
				return "", err
			}
			return fmt.Sprintf("compacted %d messages into %d", before, len(a.History())), nil
		},
	}
	if err := repl.Run(context.Background(), a, os.Stdin, os.Stdout, repl.Options{Extra: extra, Commands: cmds}); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
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
