// Command evalrun runs the task suite under one or more configurations.
//
//	evalrun --config sonnet=anthropic/claude-sonnet-5.5 --config haiku=anthropic/claude-haiku-5.5 --repeats 3
//
// A config is name=model, optionally followed by extra harness flags:
// --config 'nocache=anthropic/claude-haiku-5.5 --cache off'
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/eval"
)

type configs []eval.RunConfig

func (c *configs) String() string { return fmt.Sprint(*c) }
func (c *configs) Set(v string) error {
	name, rest, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("want name=model [flags], got %q", v)
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return fmt.Errorf("config %s has no model", name)
	}
	*c = append(*c, eval.RunConfig{Name: name, Model: fields[0], HarnessArgs: fields[1:]})
	return nil
}

func main() {
	var cfgs configs
	flag.Var(&cfgs, "config", "name=model [harness flags] (repeatable)")
	suite := flag.String("suite", "testdata/evals", "task suite directory")
	only := flag.String("tasks", "", "comma-separated task names to run (default all)")
	repeats := flag.Int("repeats", 3, "runs per task per config")
	parallel := flag.Int("parallel", 4, "concurrent runs per config")
	timeout := flag.Duration("timeout", 10*time.Minute, "per-run timeout")
	htmlOut := flag.String("html", "eval-report.html", "HTML report path (empty to skip)")
	jsonOut := flag.String("json", "eval-outcomes.json", "raw outcomes path (empty to skip)")
	bin := flag.String("harness", "", "harness binary (default: build ./cmd/harness)")
	traces := flag.String("traces", "eval-traces", "directory for per-run event traces (empty to skip)")
	flag.Parse()
	if len(cfgs) == 0 {
		cfgs.Set("haiku=anthropic/claude-haiku-5.5")
	}

	tasks, err := eval.LoadSuite(*suite)
	if err != nil {
		fail(err)
	}
	if *only != "" {
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
		tasks = sel
	}
	if *bin == "" {
		dir, _ := os.MkdirTemp("", "evalrun")
		defer os.RemoveAll(dir)
		*bin = filepath.Join(dir, "harness")
		if out, err := exec.Command("go", "build", "-o", *bin, "./cmd/harness").CombinedOutput(); err != nil {
			fail(fmt.Errorf("build harness: %v\n%s", err, out))
		}
	}
	for i := range cfgs {
		cfgs[i].Repeats, cfgs[i].Parallel, cfgs[i].Timeout, cfgs[i].TraceDir = *repeats, *parallel, *timeout, *traces
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(os.Stderr, "%d tasks × %d repeats × %d configs = %d runs\n", len(tasks), *repeats, len(cfgs), len(tasks)**repeats*len(cfgs))
	start := time.Now()
	outs, err := eval.Run(ctx, tasks, cfgs, *bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stopped early:", err)
	}
	fmt.Printf("\n%d runs in %s\n\n", len(outs), time.Since(start).Round(time.Second))
	eval.PrintSummary(os.Stdout, outs)

	var total float64
	for _, o := range outs {
		total += o.CostUSD
	}
	fmt.Printf("\ntotal model cost: $%.2f\n", total)
	for _, o := range outs {
		switch {
		case o.Err != "":
			fmt.Printf("  error %s/%s rep %d: %s\n", o.Config, o.Task, o.Rep, firstLine(o.Err))
		case !o.Pass:
			fmt.Printf("  fail  %s/%s rep %d: %s  (trace: %s)\n", o.Config, o.Task, o.Rep, lastLine(o.CheckOutput), o.Trace)
		}
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(outs, "", "  ")
		os.WriteFile(*jsonOut, b, 0o644)
	}
	if *htmlOut != "" {
		f, err := os.Create(*htmlOut)
		if err == nil {
			eval.WriteHTML(f, outs)
			f.Close()
			fmt.Println("wrote", *htmlOut)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && l != "FAIL" && !strings.HasPrefix(l, "FAIL\t") {
			return firstLine(l)
		}
	}
	return ""
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
