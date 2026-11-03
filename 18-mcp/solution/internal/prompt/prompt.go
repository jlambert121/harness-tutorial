// Package prompt assembles what the model sees before the conversation:
// the system prompt and the first-message context (environment and project instructions).
package prompt

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"harness/internal/llm"
)

//go:embed templates/*.md
var templates embed.FS

type Instruction struct {
	Path, Content string
}

type EnvInfo struct {
	Cwd      string
	Platform string
	Date     string // YYYY-MM-DD
	GitRepo  bool
}

type Section struct {
	Name   string
	Text   string
	Tokens int
}

type Prompt struct {
	Variant  string    // template used: "anthropic", "gpt", or "base"
	System   string    // stable across sessions: cacheable
	Context  string    // prepended to the first user message
	Sections []Section // for inspection: system prompt, each tool, each instruction file, environment
}

// EstimateTokens is the bytes/4 rule of thumb (Codex and opencode both use it).
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

func DetectEnv(cwd string) EnvInfo {
	_, err := exec.Command("git", "-C", cwd, "rev-parse", "--git-dir").Output()
	return EnvInfo{Cwd: cwd, Platform: runtime.GOOS, Date: time.Now().Format("2006-01-02"), GitRepo: err == nil}
}

func variant(model string) string {
	switch {
	case strings.HasPrefix(model, "anthropic/"):
		return "anthropic"
	case strings.HasPrefix(model, "openai/"):
		return "gpt"
	}
	return "base"
}

// Build renders the prompt. System contains nothing session-specific, so the
// same bytes are sent in every session; everything that varies goes in Context.
func Build(model string, env EnvInfo, instr []Instruction, tools []llm.ToolSpec) Prompt {
	v := variant(model)
	sys, _ := templates.ReadFile("templates/" + v + ".md")
	p := Prompt{Variant: v, System: strings.TrimSpace(string(sys))}
	p.Sections = append(p.Sections, Section{Name: "system prompt", Text: p.System, Tokens: EstimateTokens(p.System)})
	for _, t := range tools {
		text := t.Name + t.Description + string(t.Schema)
		p.Sections = append(p.Sections, Section{Name: "tool: " + t.Name, Text: text, Tokens: EstimateTokens(text)})
	}

	var ctx strings.Builder
	for _, in := range instr {
		fmt.Fprintf(&ctx, "<project_instructions path=%q>\n%s\n</project_instructions>\n\n", in.Path, strings.TrimSpace(in.Content))
		p.Sections = append(p.Sections, Section{Name: "instructions: " + in.Path, Text: in.Content, Tokens: EstimateTokens(in.Content)})
	}
	git := "no"
	if env.GitRepo {
		git = "yes"
	}
	envText := fmt.Sprintf("<environment>\nworking directory: %s\nplatform: %s\ngit repository: %s\ntoday's date: %s\n</environment>", env.Cwd, env.Platform, git, env.Date)
	ctx.WriteString(envText)
	p.Sections = append(p.Sections, Section{Name: "environment", Text: envText, Tokens: EstimateTokens(envText)})
	p.Context = ctx.String()
	return p
}

var instructionNames = []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}

// Discover finds instruction files: the global one in home/.config/harness,
// then one per directory from the project root down to cwd. The root is the
// nearest ancestor containing .git; without one, only cwd is searched.
// Files are cut once their combined size reaches budget bytes.
func Discover(cwd, home string, budget int) []Instruction {
	var dirs []string
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".config", "harness"))
	}
	root := gitRoot(cwd)
	var chain []string
	for d := cwd; ; d = filepath.Dir(d) {
		chain = append([]string{d}, chain...)
		if d == root || filepath.Dir(d) == d {
			break
		}
	}
	dirs = append(dirs, chain...)

	var out []Instruction
	used := 0
	for _, d := range dirs {
		for _, name := range instructionNames {
			b, err := os.ReadFile(filepath.Join(d, name))
			if err != nil {
				continue
			}
			content := string(b)
			if name == "AGENTS.override.md" && strings.TrimSpace(content) == "" {
				continue
			}
			if used >= budget {
				return out
			}
			if used+len(content) > budget {
				content = strings.ToValidUTF8(content[:budget-used], "") + "\n[truncated: instruction budget reached]"
			}
			used += len(content)
			out = append(out, Instruction{Path: filepath.Join(d, name), Content: content})
			break
		}
	}
	return out
}

func gitRoot(cwd string) string {
	for d := cwd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return cwd
		}
	}
}
