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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"harness/internal/agent"
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
	store *session.Store
	snaps *session.Snapshots
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
	cfg := agent.Config{
		Client: llm.NewGateway(o.base, os.Getenv("AI_GATEWAY_API_KEY")),
		Model:  o.model, System: p.System, Context: p.Context, Tools: reg, MaxSteps: o.maxSteps,
	}
	ap := &app{}
	var resumed []llm.Message
	if !o.noSession {
		store, err := openSession(o, absRoot)
		if err != nil {
			return nil, nil, nil, err
		}
		ap.store = store
		resumed = store.Messages(store.Leaf())
		cfg.Persist = func(m llm.Message) { store.AppendMessage(m) }
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
	cmds := map[string]repl.Command{"undo": func(ctx context.Context, _ string) (string, error) { return ap.undo(ctx, a) }}
	if err := repl.Run(context.Background(), a, os.Stdin, os.Stdout, repl.Options{Extra: extra, Commands: cmds}); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
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
	a, _, err := build(o)
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
	if err := a.Run(ctx, fs.Arg(0), agent.Tee(out, extra)); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
