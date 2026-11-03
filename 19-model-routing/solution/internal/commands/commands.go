// Package commands handles slash commands: built-ins, user prompt templates,
// and skills (instructions the model loads on demand).
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Command runs for "/name args". The returned text is shown to the user.
type Command struct {
	Name, Description, ArgumentHint string
	Run                             func(ctx context.Context, args string) (string, error)
}

type Registry struct {
	cmds map[string]Command
}

func NewRegistry() *Registry { return &Registry{cmds: map[string]Command{}} }

func (r *Registry) Register(c Command) { r.cmds[c.Name] = c }

func (r *Registry) Lookup(name string) (Command, bool) { c, ok := r.cmds[name]; return c, ok }

func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Command) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Complete returns commands whose names start with prefix, for the palette.
func (r *Registry) Complete(prefix string) []Command {
	var out []Command
	for _, c := range r.List() {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	return out
}

var nameRe = regexp.MustCompile(`^/([a-zA-Z][\w:.-]*)(?:\s+(.*))?$`)

// Parse splits "/name args". Input whose first word contains another "/"
// (a path such as /usr/bin/env) is not a command.
func Parse(input string) (name, args string, ok bool) {
	m := nameRe.FindStringSubmatch(strings.TrimSpace(input))
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// Shell runs a command for !`...` substitution; it must apply permissions.
type Shell func(ctx context.Context, command string) (string, error)

var (
	shellRe   = regexp.MustCompile("!`([^`]+)`")
	defaultRe = regexp.MustCompile(`\$\{([1-9])(?::-([^}]*))?\}`)
	posRe     = regexp.MustCompile(`\\?\$([1-9])`)
)

// Expand fills a template: $ARGUMENTS, $1..$9 (shell-style quoting),
// ${1:-default}, and !`command` replaced by the command's output.
func Expand(tmpl, args string, shell Shell) (string, error) {
	words := splitArgs(args)
	arg := func(i int) string {
		if i <= len(words) {
			return words[i-1]
		}
		return ""
	}
	// Command output is spliced in last, through placeholders, so text it
	// contains ($1, $ARGUMENTS in a shell script) is never substituted.
	var shellErr error
	var outputs []string
	out := shellRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		if shellErr != nil {
			return ""
		}
		if shell == nil {
			shellErr = errors.New("shell substitution is not available here")
			return ""
		}
		res, err := shell(context.Background(), shellRe.FindStringSubmatch(m)[1])
		if err != nil {
			shellErr = fmt.Errorf("!`%s`: %w", shellRe.FindStringSubmatch(m)[1], err)
		}
		outputs = append(outputs, res)
		return fmt.Sprintf("\x00%d\x00", len(outputs)-1)
	})
	if shellErr != nil {
		return "", shellErr
	}
	out = strings.ReplaceAll(out, "$ARGUMENTS", args)
	out = defaultRe.ReplaceAllStringFunc(out, func(m string) string {
		sm := defaultRe.FindStringSubmatch(m)
		i, _ := strconv.Atoi(sm[1])
		if v := arg(i); v != "" {
			return v
		}
		return sm[2]
	})
	out = posRe.ReplaceAllStringFunc(out, func(m string) string {
		if strings.HasPrefix(m, `\`) {
			return m[1:]
		}
		i, _ := strconv.Atoi(m[1:])
		return arg(i)
	})
	for i, o := range outputs {
		out = strings.Replace(out, fmt.Sprintf("\x00%d\x00", i), o, 1)
	}
	return out, nil
}

// splitArgs splits on whitespace, honoring single and double quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	in := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, in = r, true
		case quote == 0 && (r == ' ' || r == '\t'):
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// Template is a user-defined command: a markdown file whose body becomes the prompt.
type Template struct {
	Name, Description, ArgumentHint, Body, Path string
}

// LoadTemplates reads *.md files from each directory; later directories
// override earlier ones (project over user).
func LoadTemplates(dirs ...string) []Template {
	byName := map[string]Template{}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			meta, body := frontmatter(string(b))
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			byName[name] = Template{Name: name, Description: meta["description"], ArgumentHint: meta["argument-hint"], Body: body, Path: f}
		}
	}
	var out []Template
	for _, t := range byName {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Template) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// AddTemplates registers each template as a command that expands it and
// sends the result as a prompt.
func (r *Registry) AddTemplates(ts []Template, send func(ctx context.Context, prompt string) error, shell Shell) {
	for _, t := range ts {
		r.Register(Command{Name: t.Name, Description: t.Description, ArgumentHint: t.ArgumentHint,
			Run: func(ctx context.Context, args string) (string, error) {
				p, err := Expand(t.Body, args, shell)
				if err != nil {
					return "", err
				}
				return "", send(ctx, p)
			}})
	}
}

// frontmatter splits "---\nkey: value\n---\nbody". Values are plain strings.
func frontmatter(s string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(s, "---\n") {
		return meta, strings.TrimSpace(s)
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return meta, strings.TrimSpace(s)
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	body := s[4+end+4:]
	return meta, strings.TrimSpace(body)
}
