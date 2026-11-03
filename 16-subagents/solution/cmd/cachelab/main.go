// Command cachelab runs eval tasks under cache-friendly and cache-hostile
// configurations and compares cache hit rate, cost, and time.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"harness/internal/eval"
)

func main() {
	suite := flag.String("suite", "testdata/evals", "task suite")
	only := flag.String("tasks", "01-bugfix-paginate,04-feature-lru,08-multifile-priority,09-explore-health,14-spec-in-docs", "tasks to run")
	repeats := flag.Int("repeats", 3, "repeats per task")
	flag.Parse()

	type cfg struct{ name, model, args string }
	cfgs := []cfg{
		{"haiku manual", "anthropic/claude-haiku-5.5", "--cache manual"},
		{"haiku auto", "anthropic/claude-haiku-5.5", "--cache auto"},
		{"haiku off", "anthropic/claude-haiku-5.5", "--cache off"},
		{"haiku hostile", "anthropic/claude-haiku-5.5", "--cache manual --cache-hostile"},
		{"luna implicit", "openai/gpt-6-luna", "--cache off"},
		{"luna hostile", "openai/gpt-6-luna", "--cache off --cache-hostile"},
	}
	tasks, err := eval.LoadSuite(*suite)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	keep := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		keep[n] = true
	}
	var sel []eval.Task
	for _, t := range tasks {
		if keep[t.Name] {
			sel = append(sel, t)
		}
	}
	dir, _ := os.MkdirTemp("", "cachelab")
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	var rcs []eval.RunConfig
	for _, c := range cfgs {
		rcs = append(rcs, eval.RunConfig{Name: c.name, Model: c.model, Repeats: *repeats, Parallel: 5,
			Timeout: 10 * time.Minute, HarnessArgs: append([]string{"--no-session"}, strings.Fields(c.args)...)})
	}
	fmt.Fprintf(os.Stderr, "%d tasks × %d repeats × %d configs\n", len(sel), *repeats, len(cfgs))
	outs, _ := eval.Run(context.Background(), sel, rcs, bin)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONFIG\tPASS\tINPUT TOK/RUN\tCACHE READ\tCOST/RUN\tTIME/RUN\tCACHE READ %")
	for _, c := range cfgs {
		var in, cached, n, pass int
		var cost float64
		var wall time.Duration
		for _, o := range outs {
			if o.Config != c.name {
				continue
			}
			n++
			in += o.InTok
			cached += o.CacheRead
			cost += o.CostUSD
			wall += o.Wall
			if o.Pass {
				pass++
			}
		}
		if n == 0 {
			continue
		}
		pct := float64(cached) / float64(max(in, 1))
		fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%d\t$%.4f\t%s\t%s %.0f%%\n", c.name, pass, n, in/n, cached/n, cost/float64(n),
			(wall / time.Duration(n)).Round(100*time.Millisecond), strings.Repeat("█", int(pct*30))+strings.Repeat("·", 30-int(pct*30)), pct*100)
	}
	tw.Flush()
}
