package prompt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/llm"
	"harness/internal/prompt"
)

// tree creates files under a temp dir and returns its path.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	return root
}

func paths(root string, ins []prompt.Instruction) []string {
	var out []string
	for _, i := range ins {
		rel, err := filepath.Rel(root, i.Path)
		if err != nil {
			rel = i.Path
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestDiscoverOrderAndFirstMatch(t *testing.T) {
	base := tree(t, map[string]string{
		"home/.config/harness/AGENTS.md": "global rules",
		"repo/.git/":                     "",
		"repo/AGENTS.md":                 "root rules",
		"repo/pkg/CLAUDE.md":             "pkg claude",
		"repo/pkg/AGENTS.md":             "pkg agents",
		"repo/pkg/sub/CLAUDE.md":         "sub claude",
	})
	got := paths(base, prompt.Discover(filepath.Join(base, "repo/pkg/sub"), filepath.Join(base, "home"), 32*1024))
	want := []string{"home/.config/harness/AGENTS.md", "repo/AGENTS.md", "repo/pkg/AGENTS.md", "repo/pkg/sub/CLAUDE.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got  %v\nwant %v (global first, then root→cwd, first match per directory: AGENTS.override.md, AGENTS.md, CLAUDE.md)", got, want)
	}
}

func TestDiscoverOverrideWins(t *testing.T) {
	base := tree(t, map[string]string{"repo/.git/": "", "repo/AGENTS.md": "normal", "repo/AGENTS.override.md": "override"})
	got := prompt.Discover(filepath.Join(base, "repo"), filepath.Join(base, "nohome"), 32*1024)
	if len(got) != 1 || got[0].Content != "override" {
		t.Errorf("got %+v", got)
	}
}

func TestDiscoverStopsAtGitRoot(t *testing.T) {
	base := tree(t, map[string]string{"AGENTS.md": "above the repo", "repo/.git/": "", "repo/AGENTS.md": "repo"})
	got := prompt.Discover(filepath.Join(base, "repo"), filepath.Join(base, "nohome"), 32*1024)
	if len(got) != 1 || got[0].Content != "repo" {
		t.Errorf("files above the git root must not load: %+v", got)
	}
}

func TestDiscoverWithoutGitUsesCwdOnly(t *testing.T) {
	base := tree(t, map[string]string{"AGENTS.md": "parent", "proj/AGENTS.md": "proj"})
	got := prompt.Discover(filepath.Join(base, "proj"), filepath.Join(base, "nohome"), 32*1024)
	if len(got) != 1 || got[0].Content != "proj" {
		t.Errorf("got %+v", got)
	}
}

func TestDiscoverBudgetTruncates(t *testing.T) {
	big := strings.Repeat("rule\n", 4000)
	base := tree(t, map[string]string{"repo/.git/": "", "repo/AGENTS.md": big, "repo/a/AGENTS.md": big})
	got := prompt.Discover(filepath.Join(base, "repo/a"), filepath.Join(base, "nohome"), 30000)
	total := 0
	for _, i := range got {
		total += len(i.Content)
	}
	if total > 30000+200 {
		t.Errorf("total instruction bytes %d exceed the 30000 budget", total)
	}
	if len(got) != 2 || !strings.Contains(got[1].Content, "truncated") {
		t.Errorf("the file that overflows the budget should be cut with a note; got %d files", len(got))
	}
}

var specs = []llm.ToolSpec{
	{Name: "read", Description: "Read a file", Schema: json.RawMessage(`{"type":"object"}`)},
	{Name: "bash", Description: "Run a command", Schema: json.RawMessage(`{"type":"object"}`)},
}

func TestPromptIsDeterministic(t *testing.T) {
	env := prompt.EnvInfo{Cwd: "/w", Platform: "darwin", Date: "2026-10-20", GitRepo: true}
	a := prompt.Build("anthropic/claude-sonnet-5.5", env, nil, specs)
	env.Date = "2026-10-21"
	b := prompt.Build("anthropic/claude-sonnet-5.5", env, nil, specs)
	if a.System != b.System {
		t.Error("System must not depend on the date or anything else that changes between sessions")
	}
	if strings.Contains(a.System, "2026") || strings.Contains(a.System, "/w") {
		t.Error("environment details belong in Context, not System")
	}
	if a.Context == b.Context {
		t.Error("Context should carry the date")
	}
}

func TestModelVariantSelection(t *testing.T) {
	env := prompt.EnvInfo{Cwd: "/w"}
	claude := prompt.Build("anthropic/claude-haiku-5.5", env, nil, nil).Variant
	gpt := prompt.Build("openai/gpt-6-luna", env, nil, nil).Variant
	other := prompt.Build("deepseek/deepseek-v4.1-flash", env, nil, nil).Variant
	if claude != "anthropic" || gpt != "gpt" || other != "base" {
		t.Errorf("variants = %s %s %s", claude, gpt, other)
	}
}

func TestContextContainsEnvAndInstructions(t *testing.T) {
	env := prompt.EnvInfo{Cwd: "/work/repo", Platform: "darwin", Date: "2026-10-20", GitRepo: true}
	ins := []prompt.Instruction{{Path: "/work/repo/AGENTS.md", Content: "Use table-driven tests."}}
	p := prompt.Build("anthropic/claude-haiku-5.5", env, ins, specs)
	for _, want := range []string{"<environment>", "/work/repo", "darwin", "2026-10-20",
		`<project_instructions path="/work/repo/AGENTS.md">`, "Use table-driven tests."} {
		if !strings.Contains(p.Context, want) {
			t.Errorf("Context missing %q:\n%s", want, p.Context)
		}
	}
}

func TestSections(t *testing.T) {
	ins := []prompt.Instruction{{Path: "/r/AGENTS.md", Content: strings.Repeat("x", 400)}}
	p := prompt.Build("anthropic/claude-haiku-5.5", prompt.EnvInfo{Cwd: "/r"}, ins, specs)
	names := map[string]int{}
	for _, s := range p.Sections {
		names[s.Name] = s.Tokens
	}
	for _, want := range []string{"system prompt", "tool: bash", "tool: read", "instructions: /r/AGENTS.md", "environment"} {
		if names[want] <= 0 {
			t.Errorf("section %q missing or zero tokens; sections = %v", want, names)
		}
	}
	if names["instructions: /r/AGENTS.md"] != prompt.EstimateTokens(strings.Repeat("x", 400)) {
		t.Errorf("instruction tokens = %d", names["instructions: /r/AGENTS.md"])
	}
}

func TestEstimateTokens(t *testing.T) {
	if prompt.EstimateTokens("") != 0 || prompt.EstimateTokens("abcd") != 1 || prompt.EstimateTokens("abcde") != 2 {
		t.Error("EstimateTokens should be ceil(bytes/4)")
	}
}
