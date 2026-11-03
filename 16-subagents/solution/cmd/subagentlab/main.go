// Command subagentlab answers one codebase question inline and through
// parallel explore subagents, and compares the parent's context, total
// cost, time, and whether the answer names the right code.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

const question = "In this Go codebase, trace what happens from the moment the model's streamed response contains a tool call until the tool's result is sent back to the model. Name the files and functions involved, in order."

var expected = []string{"Execute", "agent.go", "Stream", "registry.go"}

type result struct {
	peak, subagents, toolCalls int
	cost                       float64
	wall                       time.Duration
	found                      int
}

func main() {
	corpus := flag.String("corpus", "", "codebase to ask about (e.g. <tutorial>/15-memory/solution)")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "parent model")
	n := flag.Int("n", 3, "repeats")
	flag.Parse()
	bin := filepath.Join(os.TempDir(), "subagentlab-harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	configs := []struct {
		name, prompt string
		flags        []string
	}{
		{"inline", question, []string{"--no-subagents"}},
		{"explore subagents", question + " Delegate the research to explore subagents: launch 2-3 tasks in parallel, each covering one part of the path, then combine their answers.", nil},
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONFIG\tRUN\tPARENT PEAK CONTEXT\tSUBAGENTS\tPARENT TOOL CALLS\tTOTAL COST\tTIME\tANSWER NAMES")
	for _, c := range configs {
		for i := 0; i < *n; i++ {
			r := run(bin, *corpus, *model, c.prompt, c.flags)
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t$%.4f\t%s\t%d/%d\n", c.name, i+1, r.peak, r.subagents, r.toolCalls, r.cost, r.wall.Round(time.Second), r.found, len(expected))
		}
	}
	tw.Flush()
	fmt.Printf("\nANSWER NAMES counts how many of %v the final answer mentions.\n", expected)
}

func run(bin, corpus, model, prompt string, flags []string) result {
	data, _ := os.MkdirTemp("", "sublab")
	defer os.RemoveAll(data)
	trace := filepath.Join(data, "trace.jsonl")
	args := append([]string{"exec", "--no-session", "--no-memory", "--mode", "plan", "--root", corpus, "--model", model, "--trace", trace}, flags...)
	cmd := exec.Command(bin, append(args, prompt)...)
	cmd.Env = append(os.Environ(), "HARNESS_DATA_DIR="+data)
	start := time.Now()
	cmd.Run()
	r := result{wall: time.Since(start)}
	f, err := os.Open(trace)
	if err != nil {
		return r
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var final string
	for sc.Scan() {
		var e struct {
			Kind  string
			Data  map[string]any
			Usage *struct {
				CostUSD float64 `json:"cost_usd"`
			}
			Message *struct {
				Blocks []struct{ Text string }
			}
		}
		json.Unmarshal(sc.Bytes(), &e)
		switch e.Kind {
		case "context":
			if v, ok := e.Data["estimated"].(float64); ok {
				r.peak = max(r.peak, int(v))
			}
		case "subagent":
			if e.Data["phase"] == "start" {
				r.subagents++
			}
		case "tool_start":
			r.toolCalls++
		case "message_end":
			if e.Message != nil {
				var b strings.Builder
				for _, bl := range e.Message.Blocks {
					b.WriteString(bl.Text)
				}
				if b.Len() > 0 {
					final = b.String()
				}
			}
		case "agent_end":
			if e.Usage != nil {
				r.cost = e.Usage.CostUSD
			}
		}
	}
	for _, w := range expected {
		if strings.Contains(final, w) {
			r.found++
		}
	}
	return r
}
