// Package permission decides whether a tool call may run: rules, modes,
// shell parsing, interactive approval, and an OS sandbox for commands.
package permission

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

type Mode string

const (
	ModeDefault     Mode = "default"      // reads allowed; edits and commands ask
	ModeAcceptEdits Mode = "accept-edits" // edits allowed too; commands ask
	ModePlan        Mode = "plan"         // read-only: edits and non-read-only commands denied
	ModeYolo        Mode = "yolo"         // everything allowed except explicit deny/ask rules
)

// Rule is "<action> <tool>(<pattern>)". For bash the pattern matches the
// normalized command text and * matches anything, including spaces. For file
// tools the pattern is a path glob relative to the workspace (** spans directories).
type Rule struct {
	Action  Action
	Tool    string
	Pattern string
}

func (r Rule) String() string { return fmt.Sprintf("%s %s(%s)", r.Action, r.Tool, r.Pattern) }

var ruleRe = regexp.MustCompile(`^(allow|ask|deny)\s+([a-z_][a-z0-9_]*)(?:\((.*)\))?$`)

func ParseRule(s string) (Rule, error) {
	m := ruleRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Rule{}, fmt.Errorf("bad rule %q: want <allow|ask|deny> <tool>[(<pattern>)]", s)
	}
	p := m[3]
	if p == "" {
		p = "*"
	}
	return Rule{Action: Action(m[1]), Tool: m[2], Pattern: p}, nil
}

// LoadRules reads one rule per line from each existing file; # starts a comment.
func LoadRules(paths ...string) ([]Rule, error) {
	var out []Rule
	for _, p := range paths {
		f, err := os.Open(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			r, err := ParseRule(line)
			if err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: %v", p, n, err)
			}
			out = append(out, r)
		}
		f.Close()
	}
	return out, nil
}

// DefaultRules ask before reading likely secrets (opencode does the same).
func DefaultRules() []Rule {
	return []Rule{
		{Ask, "read", "**/.env"},
		{Ask, "read", "**/.env.*"},
	}
}

// Request is one permission question: a tool and what it would touch.
// Subjects are commands for bash and workspace-relative paths for file tools.
type Request struct {
	Tool     string
	Subjects []string
	Summary  string // human-readable description for the approval prompt
}

// Evaluate returns the action for req. Each subject is decided on its own:
// any matching deny rule, else any ask rule, else any allow rule, else the
// mode default. Rule order and specificity do not matter (Claude Code's
// order). The request is denied if any subject is, asked if any is.
func Evaluate(rules []Rule, mode Mode, req Request) Action {
	result := Allow
	for _, s := range req.Subjects {
		switch evaluateOne(rules, mode, req.Tool, s) {
		case Deny:
			return Deny
		case Ask:
			result = Ask
		}
	}
	return result
}

func evaluateOne(rules []Rule, mode Mode, tool, subject string) Action {
	for _, want := range []Action{Deny, Ask, Allow} {
		for _, r := range rules {
			if r.Action == want && r.Tool == tool && Match(tool, r.Pattern, subject) {
				return want
			}
		}
	}
	return modeDefault(mode, tool, subject)
}

// MatchingRule returns the rule that decided subject, if any.
func MatchingRule(rules []Rule, tool, subject string) (Rule, bool) {
	for _, want := range []Action{Deny, Ask, Allow} {
		for _, r := range rules {
			if r.Action == want && r.Tool == tool && Match(tool, r.Pattern, subject) {
				return r, true
			}
		}
	}
	return Rule{}, false
}

var readOnlyTools = map[string]bool{"read": true, "glob": true, "grep": true, "ls": true}

func modeDefault(mode Mode, tool, subject string) Action {
	if readOnlyTools[tool] {
		return Allow
	}
	if tool == "bash" && readOnlyCommand(subject) {
		return Allow
	}
	switch mode {
	case ModeYolo:
		return Allow
	case ModePlan:
		if tool == "edit" || tool == "write" || tool == "bash" {
			return Deny
		}
		return Ask
	case ModeAcceptEdits:
		if tool == "edit" || tool == "write" {
			return Allow
		}
	}
	return Ask
}

// Match reports whether pattern matches subject for the given tool.
func Match(tool, pattern, subject string) bool {
	if tool == "bash" {
		re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
		ok, _ := regexp.MatchString(re, subject)
		return ok
	}
	return matchSegs(strings.Split(pattern, "/"), strings.Split(subject, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

var readOnlyNames = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "pwd": true, "echo": true,
	"grep": true, "rg": true, "which": true, "file": true, "stat": true, "du": true, "df": true,
	"tree": true, "sort": true, "uniq": true, "diff": true, "date": true, "true": true, "false": true,
}

var readOnlyGit = map[string]bool{"status": true, "diff": true, "log": true, "show": true, "rev-parse": true, "ls-files": true, "blame": true}

var readOnlyGo = map[string]bool{"version": true, "env": true, "list": true, "doc": true}

// readOnlyCommand is a small allowlist of commands that cannot modify
// anything, so they never prompt. A redirect would make them writers, but
// redirects are checked separately as writes.
func readOnlyCommand(cmd string) bool {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return false
	}
	switch name := f[0]; {
	case readOnlyNames[name]:
		return true
	case name == "find":
		for _, a := range f[1:] {
			switch a {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls":
				return false
			}
		}
		return true
	case name == "git":
		return len(f) > 1 && readOnlyGit[f[1]]
	case name == "go":
		return len(f) > 1 && readOnlyGo[f[1]]
	}
	return false
}
