// Command tinywindow runs one long session (one prompt per design-notes file,
// then a final question) under a small simulated context window, with and without
// compaction. The last prompt asks for a fact from the first file: it can
// only be answered if the summary carried it.
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
)

var parts = 12

func main() {
	window := flag.Int("window", 24000, "simulated context window (tokens)")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "model")
	n := flag.Int("n", 3, "repeats per configuration")
	flag.IntVar(&parts, "parts", 12, "number of files (prompts before the final question)")
	flag.Parse()
	bin := filepath.Join(os.TempDir(), "tinywindow-harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	var prompts []string
	prompts = append(prompts, "Read docs/part1.md and tell me the release codename it mentions. One sentence.")
	for i := 2; i <= parts; i++ {
		prompts = append(prompts, fmt.Sprintf("Read docs/part%d.md and summarize it in one sentence.", i))
	}
	prompts = append(prompts, "What was the release codename from part 1? Answer with the codename only.")

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONFIG\tRUN\tPROMPTS ANSWERED\tCOMPACTIONS\tERRORS\tFINAL ANSWER\tCOST")
	var example string
	for _, cfg := range []struct {
		name  string
		flags []string
	}{
		{fmt.Sprintf("%d + compaction", *window), nil},
		{fmt.Sprintf("%d no compaction", *window), []string{"--no-compact"}},
	} {
		for i := 1; i <= *n; i++ {
			work, trace := setup()
			args := append([]string{"--ui", "repl", "--no-session", "--root", work, "--model", *model,
				"--context-window", fmt.Sprint(*window), "--trace", trace}, cfg.flags...)
			cmd := exec.Command(bin, args...)
			cmd.Env = append(os.Environ(), "HARNESS_DATA_DIR="+filepath.Dir(trace))
			cmd.Stdin = strings.NewReader(strings.Join(prompts, "\n") + "\n")
			cmd.Run()
			s := scan(trace)
			answer := "-"
			if strings.Contains(strings.ToUpper(s.final), "BLUEFIN") {
				answer = "BLUEFIN ✓"
			} else if s.final != "" {
				answer = oneLine(s.final, 30) + " ✗"
			}
			fmt.Fprintf(tw, "%s\t%d\t%d/%d\t%d\t%d\t%s\t$%.4f\n", cfg.name, i, s.answered, len(prompts), s.compactions, s.errors, answer, s.cost)
			if s.compactions > 0 && example == "" {
				example = trace
			} else {
				os.RemoveAll(filepath.Dir(trace))
			}
		}
	}
	tw.Flush()
	if example != "" {
		fmt.Printf("\none compacted session, request by request (window %d):\n", *window)
		timeline(example, *window)
		os.RemoveAll(filepath.Dir(example))
	}
}

func setup() (work, trace string) {
	base, _ := os.MkdirTemp("", "tinywindow")
	work = filepath.Join(base, "work")
	os.MkdirAll(filepath.Join(work, "docs"), 0o755)
	for i := 1; i <= parts; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "# Design notes, part %d\n\n", i)
		for j := 0; j < 60; j++ {
			fmt.Fprintf(&b, "Section %d.%d: the scheduler assigns work to queue %d and retries failed jobs up to %d times before parking them for review. ", i, j, j%7, 3+j%4)
			if i == 1 && j == 37 {
				b.WriteString("The release codename for this cycle is BLUEFIN. ")
			}
			b.WriteString("\n")
		}
		os.WriteFile(filepath.Join(work, "docs", fmt.Sprintf("part%d.md", i)), []byte(b.String()), 0o644)
	}
	return work, filepath.Join(base, "trace.jsonl")
}

type stats struct {
	answered, compactions, errors int
	final                         string
	cost                          float64
}

type event struct {
	Kind  string
	Text  string
	Data  map[string]any
	Usage *struct {
		CostUSD float64 `json:"cost_usd"`
	}
	Message *struct{ Blocks []struct{ Text string } }
}

func read(path string) []event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e event
		json.Unmarshal(sc.Bytes(), &e)
		out = append(out, e)
	}
	return out
}

func scan(path string) stats {
	var s stats
	var last string
	failed := false
	for _, e := range read(path) {
		switch e.Kind {
		case "agent_start":
			last, failed = "", false
		case "compaction":
			s.compactions++
		case "error":
			if !strings.HasPrefix(e.Text, "compaction") {
				failed = true
			}
		case "message_end":
			if e.Message != nil {
				var b strings.Builder
				for _, bl := range e.Message.Blocks {
					b.WriteString(bl.Text)
				}
				if b.Len() > 0 {
					last = b.String()
				}
			}
		case "agent_end":
			if e.Usage != nil {
				s.cost += e.Usage.CostUSD
			}
			if failed {
				s.errors++
				last = ""
			} else if last != "" {
				s.answered++
			}
			s.final = last
		}
	}
	return s
}

func timeline(path string, window int) {
	prompt := 0
	for _, e := range read(path) {
		switch e.Kind {
		case "agent_start":
			prompt++
			fmt.Printf("  prompt %d\n", prompt)
		case "context":
			v, _ := e.Data["estimated"].(float64)
			fmt.Printf("    %6d %s\n", int(v), strings.Repeat("█", min(int(v)*50/window, 60)))
		case "compaction":
			fmt.Printf("    ▲ compaction (%v): %v messages → %v\n", e.Data["reason"], e.Data["before"], e.Data["after"])
		case "error":
			fmt.Printf("    ✗ %s\n", oneLine(e.Text, 90))
		}
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
