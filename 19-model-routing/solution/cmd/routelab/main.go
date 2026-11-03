// Command routelab runs the eval suite with fixed models and with routers,
// and compares pass rate, cost, cache hit rate, and how often runs escalated.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"harness/internal/eval"
)

// A change on step 1 happens before the first request, so it costs no cache.
var firstStepChange = regexp.MustCompile(`"kind":"model_change","turn":1,`)

func main() {
	main_ := flag.String("main", "anthropic/claude-sonnet-5.5", "strong model")
	small := flag.String("small", "anthropic/claude-haiku-5.5", "cheap model")
	repeats := flag.Int("repeats", 3, "repeats")
	htmlOut := flag.String("html", "routelab.html", "report")
	flag.Parse()
	tasks, _ := eval.LoadSuite("testdata/evals")
	dir, _ := os.MkdirTemp("", "routelab")
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	common := []string{"--no-session", "--no-memory", "--small-model", *small}
	cfgs := []eval.RunConfig{
		{Name: "fixed strong", Model: *main_, HarnessArgs: common},
		{Name: "fixed cheap", Model: *small, HarnessArgs: common},
		{Name: "heuristic router", Model: *main_, HarnessArgs: append(common, "--router", "heuristic")},
		{Name: "escalating router", Model: *main_, HarnessArgs: append(common, "--router", "escalate")},
	}
	for i := range cfgs {
		cfgs[i].Repeats, cfgs[i].Parallel, cfgs[i].Timeout, cfgs[i].TraceDir = *repeats, 6, 10*time.Minute, filepath.Join(dir, "traces")
	}
	fmt.Fprintf(os.Stderr, "%d tasks × %d repeats × %d configs\n", len(tasks), *repeats, len(cfgs))
	outs, _ := eval.Run(context.Background(), tasks, cfgs, bin)
	eval.PrintSummary(os.Stdout, outs)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nCONFIG\tRUNS ON STRONG MODEL\tSTEP-1 PICKS\tMID-RUN SWITCHES\tCACHE READ %\tTOTAL COST")
	for _, c := range cfgs {
		var strong, picks, switches, in, cached, n int
		var cost float64
		for _, o := range outs {
			if o.Config != c.Name {
				continue
			}
			n++
			in += o.InTok
			cached += o.CacheRead
			cost += o.CostUSD
			b, _ := os.ReadFile(o.Trace)
			s := string(b)
			sw := strings.Count(s, `"kind":"model_change"`)
			first := len(firstStepChange.FindAllString(s, -1))
			picks += first
			switches += sw - first
			usedStrong := c.Model == *main_ && c.Name != "escalating router"
			if c.Name == "heuristic router" {
				usedStrong = !strings.Contains(s, `"to":"`+*small+`"`)
			}
			if c.Name == "escalating router" {
				usedStrong = sw-first > 0
			}
			if usedStrong {
				strong++
			}
		}
		fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%d\t%.0f%%\t$%.2f\n", c.Name, strong, n, picks, switches, 100*float64(cached)/float64(max(in, 1)), cost)
	}
	tw.Flush()
	if f, err := os.Create(*htmlOut); err == nil {
		eval.WriteHTML(f, outs)
		f.Close()
	}
	for _, o := range outs {
		if !o.Pass {
			fmt.Printf("  fail %s/%s rep %d\n", o.Config, o.Task, o.Rep)
		}
	}
}
