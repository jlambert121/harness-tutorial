// Package eval runs a suite of verifiable coding tasks through the harness's
// headless mode and scores the results.
package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"harness/internal/agent"
)

// Task is a directory with task.md (the prompt), repo/ (the starting
// workspace), and check/ (hidden files copied in after the agent finishes,
// including check.sh, whose exit status decides pass or fail).
type Task struct {
	Name, Prompt, Dir string
}

type RunConfig struct {
	Name        string
	Model       string
	Repeats     int
	Parallel    int
	Timeout     time.Duration // per run; default 10 minutes
	HarnessArgs []string      // extra flags passed to "harness exec"
	TraceDir    string        // if set, each run's event trace is saved here as <config>/<task>-<rep>.jsonl
}

type Outcome struct {
	Task, Config string
	Rep          int
	Pass         bool
	Turns        int
	InTok        int
	OutTok       int
	CacheRead    int
	CostUSD      float64
	Wall         time.Duration
	Err          string // the harness failed or timed out
	CheckOutput  string // tail of check.sh output
	Trace        string // path of the saved event trace, if any
}

func LoadSuite(dir string) ([]Task, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(abs, e.Name(), "task.md"))
		if err != nil {
			continue
		}
		tasks = append(tasks, Task{Name: e.Name(), Prompt: string(bytes.TrimSpace(b)), Dir: filepath.Join(abs, e.Name())})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

// Run executes every task Repeats times under every config. Runs within a
// config execute up to Parallel at a time, each in its own temp directory.
func Run(ctx context.Context, tasks []Task, cfgs []RunConfig, harnessBin string) ([]Outcome, error) {
	var all []Outcome
	for _, cfg := range cfgs {
		reps := max(cfg.Repeats, 1)
		par := max(cfg.Parallel, 1)
		outs := make([]Outcome, 0, len(tasks)*reps)
		var mu sync.Mutex
		sem := make(chan struct{}, par)
		var wg sync.WaitGroup
		for rep := 0; rep < reps; rep++ {
			for _, t := range tasks {
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					o := runOne(ctx, t, cfg, rep, harnessBin)
					mu.Lock()
					outs = append(outs, o)
					mu.Unlock()
				}()
			}
		}
		wg.Wait()
		sort.Slice(outs, func(i, j int) bool {
			if outs[i].Rep != outs[j].Rep {
				return outs[i].Rep < outs[j].Rep
			}
			return outs[i].Task < outs[j].Task
		})
		all = append(all, outs...)
		if ctx.Err() != nil {
			return all, ctx.Err()
		}
	}
	return all, nil
}

func runOne(ctx context.Context, t Task, cfg RunConfig, rep int, bin string) Outcome {
	o := Outcome{Task: t.Name, Config: cfg.Name, Rep: rep}
	work, err := os.MkdirTemp("", "eval-"+t.Name+"-")
	if err != nil {
		o.Err = err.Error()
		return o
	}
	defer os.RemoveAll(work)
	if err := copyDir(filepath.Join(t.Dir, "repo"), work); err != nil {
		o.Err = "copy repo: " + err.Error()
		return o
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"exec", "--json", "--root", work}, cfg.HarnessArgs...)
	if cfg.TraceDir != "" {
		dir := filepath.Join(cfg.TraceDir, cfg.Name)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			o.Trace = filepath.Join(dir, fmt.Sprintf("%s-%d.jsonl", t.Name, rep))
			args = append(args, "--trace", o.Trace)
		}
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, t.Prompt)
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = work
	// Kill the harness's whole process group on timeout; otherwise a child
	// still holding stdout keeps the event reader blocked (lesson 05's bug).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, _ := cmd.StdoutPipe()
	start := time.Now()
	if err := cmd.Start(); err != nil {
		o.Err = err.Error()
		return o
	}
	parseEvents(stdout, &o)
	err = cmd.Wait()
	o.Wall = time.Since(start)
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		o.Err = fmt.Sprintf("timeout after %s", timeout)
		return o
	case err != nil:
		o.Err = fmt.Sprintf("harness: %v: %s", err, tail(stderr.String(), 500))
		return o
	}

	if err := copyDir(filepath.Join(t.Dir, "check"), work); err != nil {
		o.Err = "copy check: " + err.Error()
		return o
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelCheck()
	check := exec.CommandContext(checkCtx, "/bin/sh", "./check.sh")
	check.Dir = work
	out, err := check.CombinedOutput()
	o.Pass = err == nil
	o.CheckOutput = tail(string(out), 2000)
	return o
}

func parseEvents(r io.Reader, o *Outcome) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e agent.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Kind {
		case agent.TurnStart:
			o.Turns++
		case agent.AgentEnd:
			if u := e.Usage; u != nil {
				o.InTok, o.OutTok, o.CacheRead, o.CostUSD = u.Input, u.Output, u.CacheRead, u.CostUSD
			}
		}
	}
}

type Summary struct {
	Config    string
	Runs      int
	Passes    int
	PassRate  float64
	PassStd   float64 // sample stddev of per-repeat pass rates
	MeanTurns float64
	MeanCost  float64
	MeanWall  time.Duration
	Errors    int
}

// Summarize aggregates outcomes per config, in first-seen order.
func Summarize(outs []Outcome) []Summary {
	var order []string
	byCfg := map[string][]Outcome{}
	for _, o := range outs {
		if _, ok := byCfg[o.Config]; !ok {
			order = append(order, o.Config)
		}
		byCfg[o.Config] = append(byCfg[o.Config], o)
	}
	var sums []Summary
	for _, c := range order {
		os := byCfg[c]
		s := Summary{Config: c, Runs: len(os)}
		perRep := map[int][2]int{}
		var turns, cost float64
		var wall time.Duration
		for _, o := range os {
			r := perRep[o.Rep]
			r[1]++
			if o.Pass {
				s.Passes++
				r[0]++
			}
			perRep[o.Rep] = r
			if o.Err != "" {
				s.Errors++
			}
			turns += float64(o.Turns)
			cost += o.CostUSD
			wall += o.Wall
		}
		n := float64(len(os))
		s.PassRate = float64(s.Passes) / n
		s.MeanTurns, s.MeanCost, s.MeanWall = turns/n, cost/n, wall/time.Duration(len(os))
		var rates []float64
		for _, r := range perRep {
			rates = append(rates, float64(r[0])/float64(r[1]))
		}
		s.PassStd = stddev(rates)
		sums = append(sums, s)
	}
	return sums
}

// ByTask returns task → config → pass rate.
func ByTask(outs []Outcome) map[string]map[string]float64 {
	counts := map[string]map[string][2]int{}
	for _, o := range outs {
		if counts[o.Task] == nil {
			counts[o.Task] = map[string][2]int{}
		}
		c := counts[o.Task][o.Config]
		c[1]++
		if o.Pass {
			c[0]++
		}
		counts[o.Task][o.Config] = c
	}
	out := map[string]map[string]float64{}
	for t, m := range counts {
		out[t] = map[string]float64{}
		for cfg, c := range m {
			out[t][cfg] = float64(c[0]) / float64(c[1])
		}
	}
	return out
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var mean float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
