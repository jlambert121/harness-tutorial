// Command inspect shows what the model receives before the first user word:
// system prompt, tool definitions, project instructions, and environment, by token cost.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/prompt"
	"harness/internal/tools"
)

func main() {
	model := flag.String("model", "anthropic/claude-sonnet-5.5", "model id (selects the prompt variant)")
	root := flag.String("root", ".", "workspace root")
	show := flag.Bool("show", false, "print the full system prompt and context")
	flag.Parse()

	abs, _ := filepath.Abs(*root)
	reg := tools.NewRegistry(&tools.Env{Root: abs})
	for _, t := range []tools.Tool{tools.Read(), tools.Glob(), tools.Grep(), tools.Ls(), tools.Edit(), tools.Write(), tools.Bash()} {
		reg.Register(t)
	}
	home, _ := os.UserHomeDir()
	p := prompt.Build(*model, prompt.DetectEnv(abs), prompt.Discover(abs, home, 32*1024), reg.Specs())

	top, total, stable := 0, 0, 0
	for _, s := range p.Sections {
		top = max(top, s.Tokens)
		total += s.Tokens
		if s.Name == "system prompt" || strings.HasPrefix(s.Name, "tool: ") {
			stable += s.Tokens
		}
	}
	fmt.Printf("model %s → prompt variant %q\n\n", *model, p.Variant)
	for _, s := range p.Sections {
		n := s.Tokens * 40 / max(top, 1)
		fmt.Printf("  %-44s %5d  %s\n", trim(s.Name, 44), s.Tokens, strings.Repeat("█", n))
	}
	fmt.Printf("\n  %-44s %5d tokens (estimated, bytes/4)\n", "total before the first user word", total)
	fmt.Printf("  %-44s %5d tokens (identical every session → cacheable)\n", "  system prompt + tools", stable)
	fmt.Printf("  %-44s %5d tokens (varies by project and day)\n", "  first-message context", total-stable)
	if *show {
		fmt.Printf("\n===== SYSTEM =====\n%s\n\n===== CONTEXT (first user message) =====\n%s\n", p.System, p.Context)
	}
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}
