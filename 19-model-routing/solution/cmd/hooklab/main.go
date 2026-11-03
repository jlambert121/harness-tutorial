// Command hooklab runs the eval suite with and without the lint and
// test-on-stop hooks, for one or more models.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/eval"
)

func main() {
	hooksDir := flag.String("hooks-dir", "", "directory with hooks.json and the scripts (<lesson>/hooks)")
	models := flag.String("models", "anthropic/claude-haiku-5.5,zai/glm-5.3-flash", "comma-separated models")
	repeats := flag.Int("repeats", 3, "repeats")
	flag.Parse()
	abs, _ := filepath.Abs(*hooksDir)
	os.Setenv("HOOKS_DIR", abs)
	tasks, _ := eval.LoadSuite("testdata/evals")
	dir, _ := os.MkdirTemp("", "hooklab")
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	var cfgs []eval.RunConfig
	for _, m := range strings.Split(*models, ",") {
		short := m[strings.Index(m, "/")+1:]
		cfgs = append(cfgs,
			eval.RunConfig{Name: short, Model: m, HarnessArgs: []string{"--no-session", "--no-memory"}},
			eval.RunConfig{Name: short + " + hooks", Model: m, HarnessArgs: []string{"--no-session", "--no-memory", "--hooks", filepath.Join(abs, "hooks.json")}},
		)
	}
	for i := range cfgs {
		cfgs[i].Repeats, cfgs[i].Parallel, cfgs[i].Timeout, cfgs[i].TraceDir = *repeats, 6, 10*time.Minute, filepath.Join(dir, "traces")
	}
	fmt.Fprintf(os.Stderr, "%d tasks × %d repeats × %d configs\n", len(tasks), *repeats, len(cfgs))
	outs, _ := eval.Run(context.Background(), tasks, cfgs, bin)
	eval.PrintSummary(os.Stdout, outs)
	stops := map[string]int{}
	for _, o := range outs {
		b, _ := os.ReadFile(o.Trace)
		stops[o.Config] += strings.Count(string(b), `"source":"stop_hook"`)
	}
	fmt.Println("\nstop-hook interventions (the hook sent the model back because go test failed):")
	for _, c := range cfgs {
		if strings.HasSuffix(c.Name, "hooks") {
			fmt.Printf("  %-28s %d\n", c.Name, stops[c.Name])
		}
	}
	for _, o := range outs {
		if !o.Pass {
			fmt.Printf("  fail %s/%s rep %d: %s\n", o.Config, o.Task, o.Rep, firstLine(o.CheckOutput+o.Err))
		}
	}
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			if len(l) > 110 {
				l = l[:110] + "…"
			}
			return l
		}
	}
	return ""
}
