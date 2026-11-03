package permission

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Command is one simple command found in a shell script.
type Command struct {
	Text string   // normalized: words joined by single spaces, wrappers stripped
	Args []string // literal words; empty when the command name is dynamic
}

// Parsed is what a shell script would run.
type Parsed struct {
	Commands []Command
	Writes   []string // output redirect targets
	// Dynamic means part of the script cannot be known before it runs
	// (command substitution, eval, a variable as a command name). Rules
	// cannot vouch for such scripts, so a person must approve them.
	Dynamic bool
}

var wrappers = map[string]bool{"timeout": true, "nice": true, "nohup": true, "env": true, "time": true, "command": true}

// SplitCommand parses a bash script into the simple commands it would run.
// It descends into &&, ||, ;, pipes, subshells, blocks, command
// substitutions, and literal sh -c / bash -c scripts.
func SplitCommand(script string) (Parsed, error) {
	var p Parsed
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		return p, fmt.Errorf("cannot parse command: %v", err)
	}
	p.walk(f)
	return p, nil
}

func (p *Parsed) walk(node syntax.Node) {
	syntax.Walk(node, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Stmt:
			for _, r := range x.Redirs {
				if isOutput(r.Op) && r.Word != nil {
					if t, ok := literal(r.Word); ok {
						if t != "/dev/null" && t != "/dev/stderr" && t != "/dev/stdout" {
							p.Writes = append(p.Writes, t)
						}
					} else {
						p.Dynamic = true
					}
				}
			}
		case *syntax.CmdSubst, *syntax.ProcSubst:
			p.Dynamic = true
		case *syntax.CallExpr:
			p.call(x)
		}
		return true
	})
}

func (p *Parsed) call(x *syntax.CallExpr) {
	var words []string
	dynamic := false
	for _, w := range x.Args {
		s, ok := literal(w)
		if !ok {
			dynamic = true
			s = w.Lit()
			if s == "" {
				s = "$…"
			}
		}
		words = append(words, s)
	}
	if len(words) == 0 {
		return
	}
	if stripped := stripWrappers(words); len(stripped) > 0 {
		words = stripped
	}
	if _, ok := literal(x.Args[len(x.Args)-len(words)]); !ok {
		p.Dynamic = true
	}
	if words[0] == "eval" || words[0] == "source" || words[0] == "." {
		p.Dynamic = true
	}
	if (words[0] == "sh" || words[0] == "bash" || words[0] == "zsh") && len(words) >= 3 && words[1] == "-c" && !dynamic {
		if inner, err := SplitCommand(words[2]); err == nil {
			p.Commands = append(p.Commands, inner.Commands...)
			p.Writes = append(p.Writes, inner.Writes...)
			p.Dynamic = p.Dynamic || inner.Dynamic
			return
		}
		p.Dynamic = true
	}
	p.Commands = append(p.Commands, Command{Text: quoteJoin(words), Args: words})
}

func stripWrappers(w []string) []string {
	for len(w) > 0 {
		switch {
		case strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "="):
			w = w[1:]
		case wrappers[w[0]]:
			name := w[0]
			w = w[1:]
			for len(w) > 0 && (strings.HasPrefix(w[0], "-") || (name == "timeout" && isDuration(w[0])) || (name == "env" && strings.Contains(w[0], "="))) {
				if name == "nice" && w[0] == "-n" && len(w) > 1 {
					w = w[1:]
				}
				w = w[1:]
			}
		default:
			return w
		}
	}
	return w
}

func isDuration(s string) bool {
	s = strings.TrimRight(s, "smhd")
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func isOutput(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.RdrClob:
		return true
	}
	return false
}

// literal returns the word's value if it contains no expansions.
func literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch x := part.(type) {
		case *syntax.Lit:
			b.WriteString(x.Value)
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				l, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(l.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func quoteJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		if strings.ContainsAny(w, " \t\n'\"") {
			out[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		} else {
			out[i] = w
		}
	}
	return strings.Join(out, " ")
}
