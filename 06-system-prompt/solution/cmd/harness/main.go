// Command harness runs the coding agent in a line REPL.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/prompt"
	"harness/internal/tools"
	"harness/internal/trace"
	"harness/internal/ui/repl"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	model := flag.String("model", env("HARNESS_MODEL", "anthropic/claude-sonnet-5.5"), "gateway model id")
	base := flag.String("base", env("HARNESS_BASE_URL", "https://ai-gateway.vercel.sh/v1"), "gateway base URL")
	root := flag.String("root", ".", "workspace root")
	maxSteps := flag.Int("max-steps", 50, "max model calls per prompt (0 = unlimited)")
	tracePath := flag.String("trace", "", "write events as JSONL to this file")
	yes := flag.Bool("yes", false, "approve every bash command without asking (throwaway workspaces only)")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reg := tools.NewRegistry(&tools.Env{Root: absRoot, Reads: tools.NewReadTracker()})
	for _, t := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls(), tools.Edit(), tools.Write(), tools.Bash()} {
		reg.Register(t)
	}

	if *yes {
		reg.Env().Confirm = func(context.Context, string) bool { return true }
	}
	home, _ := os.UserHomeDir()
	instructions := prompt.Discover(absRoot, home, 32*1024)
	p := prompt.Build(*model, prompt.DetectEnv(absRoot), instructions, reg.Specs())
	a := agent.New(agent.Config{
		Client: llm.NewGateway(*base, os.Getenv("AI_GATEWAY_API_KEY")),
		Model:  *model, System: p.System, Context: p.Context, Tools: reg, MaxSteps: *maxSteps,
	})

	var extra agent.Sink
	if *tracePath != "" {
		f, err := os.Create(*tracePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		extra = trace.NewRecorder(f)
	}
	fmt.Printf("model %s · workspace %s — Ctrl-C cancels, Ctrl-D exits\n", *model, absRoot)
	for _, in := range instructions {
		fmt.Printf("loaded %s\n", in.Path)
	}
	if err := repl.Run(context.Background(), a, os.Stdin, os.Stdout, extra); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
