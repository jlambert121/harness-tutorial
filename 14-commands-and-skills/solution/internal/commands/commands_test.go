package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/commands"
	"harness/internal/tools"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in, name, args string
		ok             bool
	}{
		{"/help", "help", "", true},
		{"/compact focus on the parser", "compact", "focus on the parser", true},
		{"/skill:go-testing write tests", "skill:go-testing", "write tests", true},
		{"/usr/bin/env is a path", "", "", false},
		{"/ space", "", "", false},
		{"no slash", "", "", false},
		{"  /trimmed  ", "trimmed", "", true},
	}
	for _, c := range cases {
		name, args, ok := commands.Parse(c.in)
		if name != c.name || args != c.args || ok != c.ok {
			t.Errorf("Parse(%q) = %q, %q, %v; want %q, %q, %v", c.in, name, args, ok, c.name, c.args, c.ok)
		}
	}
}

func TestExpandArgs(t *testing.T) {
	cases := []struct{ tmpl, args, want string }{
		{"Review $ARGUMENTS carefully", "the parser", "Review the parser carefully"},
		{"Compare $1 with $2", `main.go "old file.go"`, "Compare main.go with old file.go"},
		{"Focus: ${1:-everything}", "", "Focus: everything"},
		{"Focus: ${1:-everything}", "tests", "Focus: tests"},
		{"Missing $3 stays empty", "a b", "Missing  stays empty"},
		{"Cost: $5 is not an arg when written \\$5", "", "Cost:  is not an arg when written $5"},
	}
	for _, c := range cases {
		got, err := commands.Expand(c.tmpl, c.args, nil)
		if err != nil || got != c.want {
			t.Errorf("Expand(%q, %q) = %q, %v; want %q", c.tmpl, c.args, got, err, c.want)
		}
	}
}

func TestExpandShell(t *testing.T) {
	var ran []string
	shell := func(ctx context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		if cmd == "fail" {
			return "", errors.New("denied")
		}
		return "M main.go\n", nil
	}
	got, err := commands.Expand("Changed files:\n!`git status --short`\nReview them.", "", shell)
	if err != nil || got != "Changed files:\nM main.go\n\nReview them." || len(ran) != 1 || ran[0] != "git status --short" {
		t.Errorf("got %q err %v ran %v", got, err, ran)
	}
	if _, err := commands.Expand("x !`fail` y", "", shell); err == nil {
		t.Error("a failing or denied shell substitution must stop the command")
	}
	if _, err := commands.Expand("x !`ls` y", "", nil); err == nil {
		t.Error("shell substitution without a runner must be refused")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(content), 0o644)
}

func TestLoadTemplates(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	write(t, filepath.Join(user, "review.md"), "---\ndescription: Review code\nargument-hint: <path>\n---\nReview $ARGUMENTS.\n")
	write(t, filepath.Join(project, "review.md"), "---\ndescription: Project review\n---\nProject review of $ARGUMENTS.\n")
	write(t, filepath.Join(project, "plain.md"), "No frontmatter, just $ARGUMENTS.")
	write(t, filepath.Join(project, "notes.txt"), "ignored")
	ts := commands.LoadTemplates(user, project)
	if len(ts) != 2 {
		t.Fatalf("templates = %+v", ts)
	}
	byName := map[string]commands.Template{}
	for _, tm := range ts {
		byName[tm.Name] = tm
	}
	if r := byName["review"]; r.Description != "Project review" || !strings.HasPrefix(r.Body, "Project review of") {
		t.Errorf("later directories override earlier ones: %+v", r)
	}
	if p := byName["plain"]; p.Body != "No frontmatter, just $ARGUMENTS." || p.Description != "" {
		t.Errorf("plain = %+v", p)
	}
}

func TestRegistry(t *testing.T) {
	r := commands.NewRegistry()
	r.Register(commands.Command{Name: "compact", Description: "Summarize history"})
	r.Register(commands.Command{Name: "context", Description: "Show context use"})
	r.Register(commands.Command{Name: "help", Description: "List commands"})
	if _, ok := r.Lookup("compact"); !ok {
		t.Error("Lookup")
	}
	var names []string
	for _, c := range r.Complete("co") {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "compact,context" {
		t.Errorf("Complete(co) = %v", names)
	}
	if len(r.List()) != 3 || r.List()[0].Name != "compact" {
		t.Errorf("List must be sorted: %+v", r.List())
	}
}

func TestTemplateCommandSendsExpandedPrompt(t *testing.T) {
	r := commands.NewRegistry()
	var sent string
	r.AddTemplates([]commands.Template{{Name: "review", Description: "Review", Body: "Review $1 for bugs."}},
		func(ctx context.Context, prompt string) error { sent = prompt; return nil }, nil)
	c, ok := r.Lookup("review")
	if !ok {
		t.Fatal("template not registered")
	}
	if _, err := c.Run(context.Background(), "parser.go"); err != nil || sent != "Review parser.go for bugs." {
		t.Errorf("sent %q err %v", sent, err)
	}
}

func TestSkills(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go-testing", "SKILL.md"), "---\nname: go-testing\ndescription: How to write table-driven Go tests in this repo\n---\n# Go testing\nUse got/want.\n")
	write(t, filepath.Join(dir, "go-testing", "examples.md"), "example")
	write(t, filepath.Join(dir, "broken", "SKILL.md"), "no frontmatter")
	skills := commands.LoadSkills(dir)
	if len(skills) != 1 || skills[0].Name != "go-testing" {
		t.Fatalf("skills = %+v (SKILL.md without name/description is skipped)", skills)
	}
	listing := commands.SkillListing(skills)
	if !strings.Contains(listing, "go-testing") || !strings.Contains(listing, "table-driven") || strings.Contains(listing, "Use got/want") {
		t.Errorf("the listing holds only names and descriptions:\n%s", listing)
	}

	tool := commands.SkillTool(skills)
	res := tool.Run(context.Background(), &tools.Env{}, json.RawMessage(`{"name":"go-testing"}`))
	if res.IsError || !strings.Contains(res.Content, "Use got/want") || !strings.Contains(res.Content, "examples.md") {
		t.Errorf("skill tool should return the body and list supporting files: %+v", res)
	}
	res = tool.Run(context.Background(), &tools.Env{}, json.RawMessage(`{"name":"nope"}`))
	if !res.IsError || !strings.Contains(res.Content, "go-testing") {
		t.Errorf("unknown skill should list the available ones: %+v", res)
	}
}

func TestExpandLeavesShellOutputAlone(t *testing.T) {
	shell := func(ctx context.Context, cmd string) (string, error) { return `echo "$1 and $ARGUMENTS"`, nil }
	got, err := commands.Expand("Script:\n!`cat run.sh`\nArgs: $1", "first", shell)
	if err != nil || got != "Script:\necho \"$1 and $ARGUMENTS\"\nArgs: first" {
		t.Errorf("command output must not be treated as part of the template: %q", got)
	}
}
