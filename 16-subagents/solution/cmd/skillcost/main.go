// Command skillcost compares the context cost of listing skills against
// inlining them, and (with -live) checks that a model loads the right skill.
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

	"harness/internal/commands"
)

func main() {
	dir := flag.String("skills", "", "directory of skills (e.g. <lesson>/sample/.harness/skills)")
	live := flag.Bool("live", false, "run a real task and report which skill the model loaded")
	repo := flag.String("repo", "", "-live: sample repo to copy (e.g. <path>/06-system-prompt/sample-repo)")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "-live: model")
	flag.Parse()
	skills := commands.LoadSkills(*dir)
	if len(skills) == 0 {
		fmt.Fprintln(os.Stderr, "no skills found in", *dir)
		os.Exit(1)
	}
	listing := commands.SkillListing(skills)
	var all strings.Builder
	for _, s := range skills {
		b, _ := os.ReadFile(s.Path)
		all.Write(b)
	}
	lt, at := tokens(listing), tokens(all.String())
	fmt.Printf("%d skills\n", len(skills))
	fmt.Printf("  listing only (names + descriptions) %6d tokens  %s\n", lt, strings.Repeat("█", lt*50/at))
	fmt.Printf("  every SKILL.md inlined              %6d tokens  %s\n", at, strings.Repeat("█", 50))
	fmt.Printf("  saved per request: %d tokens (%.0f%%)\n", at-lt, 100*float64(at-lt)/float64(at))

	if !*live {
		return
	}
	work, _ := os.MkdirTemp("", "skillcost")
	defer os.RemoveAll(work)
	exec.Command("cp", "-R", *repo+"/.", work).Run()
	exec.Command("cp", "-R", filepath.Dir(*dir), filepath.Join(work, ".harness")).Run()
	exec.Command("git", "-C", work, "init", "-q").Run()
	trace := filepath.Join(work, "..", "skillcost-trace.jsonl")
	prompt := "Add tests for WordCount covering its edge cases."
	cmd := exec.Command("go", "run", "./cmd/harness", "exec", "--no-session", "--root", work, "--model", *model, "--trace", trace, prompt)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("run failed:", err)
	}
	fmt.Printf("\nlive (%s): %q\n", *model, prompt)
	f, _ := os.Open(trace)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e struct {
			Kind string
			Call *struct {
				Name string
				Args json.RawMessage
			}
		}
		json.Unmarshal(sc.Bytes(), &e)
		if e.Kind == "tool_start" && e.Call != nil {
			fmt.Printf("  → %s %s\n", e.Call.Name, e.Call.Args)
		}
	}
	test, _ := os.ReadFile(filepath.Join(work, "textkit_test.go"))
	fmt.Printf("  test uses t.Run with named cases (the skill's convention): %v\n", strings.Contains(string(test), "t.Run(tt.name"))
}

func tokens(s string) int { return (len(s) + 3) / 4 }
