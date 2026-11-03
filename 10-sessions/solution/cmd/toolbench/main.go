// Command toolbench measures parallel tool execution offline and, with
// -live, shows a model recovering from a tool error and batching calls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
	"harness/internal/tools"
)

var patterns = []string{`func \(\w+ \*Reader\)`, `sync\.Mutex`, `TODO`, `errors\.New\("`, `context\.Context`, `unsafe\.Pointer`, `go:linkname`, `(?i)deprecated`}

func main() {
	live := flag.Bool("live", false, "also run the live model demos")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "model for -live")
	sample := flag.String("sample", "../sample", "workspace for -live (lesson 02's sample directory)")
	reps := flag.Int("n", 3, "repetitions")
	flag.Parse()

	goroot, _ := exec.Command("go", "env", "GOROOT").Output()
	root := filepath.Join(strings.TrimSpace(string(goroot)), "src")
	fmt.Printf("Offline: %d grep calls in one batch over %s\n\n", len(patterns), root)
	var calls []llm.ToolCall
	for i, p := range patterns {
		b, _ := json.Marshal(map[string]string{"pattern": p, "mode": "count"})
		calls = append(calls, llm.ToolCall{ID: fmt.Sprint(i), Name: "grep", Args: b})
	}
	times := map[bool][]time.Duration{}
	for i := 0; i < *reps; i++ {
		for _, par := range []bool{false, true} {
			reg := tools.NewRegistry(&tools.Env{Root: root})
			reg.Register(tools.Grep())
			reg.Parallel = par
			start := time.Now()
			reg.Execute(context.Background(), calls, nil, nil)
			times[par] = append(times[par], time.Since(start))
		}
	}
	seq, par := best(times[false]), best(times[true])
	fmt.Printf("  sequential  %s %v\n", bar(seq, seq), seq.Round(time.Millisecond))
	fmt.Printf("  parallel    %s %v  (%.1fx faster)\n", bar(par, seq), par.Round(time.Millisecond), float64(seq)/float64(par))
	fmt.Println("  (best of", *reps, "runs each)")

	if !*live {
		fmt.Println("\nRun with -live to see a model recover from a tool error and batch independent calls.")
		return
	}
	abs, _ := filepath.Abs(*sample)
	client := llm.NewGateway("https://ai-gateway.vercel.sh/v1", os.Getenv("AI_GATEWAY_API_KEY"))
	for _, prompt := range []string{
		"Read Settings.go and tell me what port the app uses by default.",
		"Without reading main.go, find where Defaults is defined and where the Config type is defined, then quote both definitions.",
	} {
		reg := tools.NewRegistry(&tools.Env{Root: abs})
		for _, t := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls()} {
			reg.Register(t)
		}
		a := agent.New(agent.Config{Client: client, Model: *model, Tools: reg, MaxSteps: 10,
			System: "You are a coding assistant. When several tool calls are independent, make them in the same response."})
		fmt.Printf("\nLive (%s): %s\n", *model, prompt)
		perTurn := map[int]int{}
		a.Run(context.Background(), prompt, func(e agent.Event) {
			switch e.Kind {
			case agent.ToolStart:
				perTurn[e.Turn]++
				fmt.Printf("  turn %d → %s %s\n", e.Turn, e.Call.Name, e.Call.Args)
			case agent.ToolEnd:
				mark := "ok"
				if e.Result.IsError {
					mark = "ERROR"
				}
				first := strings.SplitN(e.Result.Content, "\n", 2)[0]
				if len(first) > 110 {
					first = first[:110] + "…"
				}
				fmt.Printf("         ← %s: %s\n", mark, first)
			case agent.AgentEnd:
				fmt.Printf("  tool calls per turn: %v · $%.5f\n", perTurn, e.Usage.CostUSD)
			}
		})
	}
}

func best(ds []time.Duration) time.Duration {
	b := ds[0]
	for _, d := range ds {
		b = min(b, d)
	}
	return b
}

func bar(d, max time.Duration) string {
	n := int(float64(d) / float64(max) * 40)
	return strings.Repeat("█", n) + strings.Repeat(" ", 40-n)
}
