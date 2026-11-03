package eval_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/eval"
)

func TestLoadSuite(t *testing.T) {
	tasks, err := eval.LoadSuite("testdata/suite")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Name != "alpha" || tasks[1].Name != "beta" {
		t.Fatalf("tasks = %+v; directories without task.md are skipped, order is by name", tasks)
	}
	if tasks[0].Prompt != "Write done into file.txt." {
		t.Errorf("prompt = %q (trimmed task.md)", tasks[0].Prompt)
	}
	if !filepath.IsAbs(tasks[0].Dir) {
		t.Errorf("Dir should be absolute: %q", tasks[0].Dir)
	}
}

func TestRunHidesChecksAndScores(t *testing.T) {
	tasks, _ := eval.LoadSuite("testdata/suite")
	bin, _ := filepath.Abs("testdata/fake-harness.sh")
	traces := t.TempDir()
	outs, err := eval.Run(context.Background(), tasks, []eval.RunConfig{{Name: "fake", Model: "m", Repeats: 2, Parallel: 2, TraceDir: traces}}, bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 4 {
		t.Fatalf("outcomes = %d, want 2 tasks × 2 repeats", len(outs))
	}
	for _, o := range outs {
		if o.Err != "" {
			t.Errorf("%s rep %d: %s", o.Task, o.Rep, o.Err)
		}
		want := o.Task == "alpha"
		if o.Pass != want {
			t.Errorf("%s rep %d: pass=%v, want %v", o.Task, o.Rep, o.Pass, want)
		}
		if o.Turns != 2 || o.InTok != 1000 || o.OutTok != 100 || o.CacheRead != 400 || o.CostUSD != 0.002 || o.Config != "fake" {
			t.Errorf("metrics not parsed from the event stream: %+v", o)
		}
		if o.Wall <= 0 {
			t.Errorf("wall time not recorded: %+v", o)
		}
		if want := filepath.Join(traces, "fake", fmt.Sprintf("%s-%d.jsonl", o.Task, o.Rep)); o.Trace != want {
			t.Errorf("Trace = %q, want %q", o.Trace, want)
		} else if b, err := os.ReadFile(o.Trace); err != nil || !strings.Contains(string(b), "agent_start") {
			t.Errorf("trace not written via --trace: %v", err)
		}
	}
	b, _ := os.ReadFile("testdata/suite/alpha/repo/file.txt")
	if string(b) != "start\n" {
		t.Errorf("the task's source repo was modified: %q", b)
	}
}

func TestRunRecordsHarnessFailure(t *testing.T) {
	tasks, _ := eval.LoadSuite("testdata/suite")
	outs, _ := eval.Run(context.Background(), tasks[:1], []eval.RunConfig{{Name: "bad", Repeats: 1}}, "/bin/false")
	if len(outs) != 1 || outs[0].Pass || outs[0].Err == "" {
		t.Errorf("a harness that exits non-zero should be recorded as an error: %+v", outs)
	}
}

func TestRunTimeout(t *testing.T) {
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow.sh")
	os.WriteFile(slow, []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	tasks, _ := eval.LoadSuite("testdata/suite")
	start := time.Now()
	outs, _ := eval.Run(context.Background(), tasks[:1], []eval.RunConfig{{Name: "slow", Repeats: 1, Timeout: 300 * time.Millisecond}}, slow)
	if time.Since(start) > 5*time.Second || !strings.Contains(outs[0].Err, "timeout") {
		t.Errorf("timeout not enforced: %+v after %v", outs, time.Since(start))
	}
}

func TestSummarize(t *testing.T) {
	var outs []eval.Outcome
	// Config A: rep 0 passes 2/2, rep 1 passes 1/2, rep 2 passes 0/2 → per-repeat rates 1, 0.5, 0.
	for rep, passes := range []int{2, 1, 0} {
		for i, task := range []string{"t1", "t2"} {
			outs = append(outs, eval.Outcome{Config: "A", Task: task, Rep: rep, Pass: i < passes, Turns: 4, CostUSD: 0.01, Wall: time.Second})
		}
	}
	outs = append(outs, eval.Outcome{Config: "B", Task: "t1", Rep: 0, Pass: true, Turns: 2, CostUSD: 0.03, Wall: 2 * time.Second})
	sums := eval.Summarize(outs)
	if len(sums) != 2 || sums[0].Config != "A" || sums[1].Config != "B" {
		t.Fatalf("summaries = %+v (one per config, in first-seen order)", sums)
	}
	a := sums[0]
	if a.Runs != 6 || a.Passes != 3 || a.PassRate != 0.5 {
		t.Errorf("A = %+v", a)
	}
	if math.Abs(a.PassStd-0.5) > 1e-9 {
		t.Errorf("PassStd = %v; want the sample stddev of per-repeat pass rates (1, 0.5, 0) = 0.5", a.PassStd)
	}
	if a.MeanTurns != 4 || math.Abs(a.MeanCost-0.01) > 1e-12 || a.MeanWall != time.Second {
		t.Errorf("A means = %+v", a)
	}
	if sums[1].PassStd != 0 {
		t.Errorf("a single repeat has no spread: %+v", sums[1])
	}
}

func TestByTask(t *testing.T) {
	outs := []eval.Outcome{
		{Config: "A", Task: "t1", Pass: true}, {Config: "A", Task: "t1", Pass: false},
		{Config: "A", Task: "t2", Pass: true},
	}
	m := eval.ByTask(outs)
	if m["t1"]["A"] != 0.5 || m["t2"]["A"] != 1 {
		t.Errorf("ByTask = %v", m)
	}
}
