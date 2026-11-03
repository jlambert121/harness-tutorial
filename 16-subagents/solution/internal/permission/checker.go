package permission

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type Decision struct {
	Action   Action // Allow or Deny
	Remember bool   // allow this kind of request for the rest of the session
	Feedback string // optional message for the model (why it was denied, what to do instead)
}

// Approver asks a person. UIs implement it; headless runs have none.
type Approver interface {
	Approve(ctx context.Context, r Request) Decision
}

type ApproverFunc func(ctx context.Context, r Request) Decision

func (f ApproverFunc) Approve(ctx context.Context, r Request) Decision { return f(ctx, r) }

// Checker applies rules and the mode, asks the Approver when needed, and
// remembers "always allow" answers for the session.
type Checker struct {
	mu       sync.Mutex
	mode     Mode
	rules    []Rule
	session  []Rule
	approver Approver
	lastCall string
	repeats  int
}

func NewChecker(mode Mode, rules []Rule, approver Approver) *Checker {
	return &Checker{mode: mode, rules: rules, approver: approver}
}

func (c *Checker) Mode() Mode { c.mu.Lock(); defer c.mu.Unlock(); return c.mode }

func (c *Checker) SetMode(m Mode) { c.mu.Lock(); c.mode = m; c.mu.Unlock() }

func (c *Checker) SetApprover(a Approver) { c.mu.Lock(); c.approver = a; c.mu.Unlock() }

func (c *Checker) SessionRules() []Rule {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Rule(nil), c.session...)
}

func (c *Checker) allRules() []Rule {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(append([]Rule(nil), c.rules...), c.session...)
}

// Check decides a file-tool request. It returns whether the call may run and
// a note for the tool result explaining a denial.
func (c *Checker) Check(ctx context.Context, req Request) (bool, string) {
	rules := c.allRules()
	switch Evaluate(rules, c.Mode(), req) {
	case Allow:
		return true, ""
	case Deny:
		return false, c.denyNote(rules, req)
	}
	return c.ask(ctx, req, func(s string) string { return s })
}

// CheckBash decides a shell command by checking every simple command in it
// and every file it redirects output to. Only the parts that are not already
// allowed are put to the approver.
func (c *Checker) CheckBash(ctx context.Context, command string) (bool, string) {
	p, err := SplitCommand(command)
	if err != nil {
		return c.ask(ctx, Request{Tool: "bash", Subjects: []string{command}, Summary: command + "\n(" + err.Error() + ")"}, nil)
	}
	rules, mode := c.allRules(), c.Mode()
	var pending []string
	for _, cmd := range p.Commands {
		req := Request{Tool: "bash", Subjects: []string{cmd.Text}}
		switch Evaluate(rules, mode, req) {
		case Deny:
			return false, c.denyNote(rules, req)
		case Ask:
			pending = append(pending, cmd.Text)
		}
	}
	for _, w := range p.Writes {
		req := Request{Tool: "write", Subjects: []string{w}}
		switch Evaluate(rules, mode, req) {
		case Deny:
			return false, c.denyNote(rules, req) + " (output redirect)"
		case Ask:
			pending = append(pending, "> "+w)
		}
	}
	// In yolo mode the sandbox, not a person, is the safeguard, so commands
	// that cannot be analyzed statically still run.
	if p.Dynamic && mode != ModeYolo {
		pending = []string{command}
	}
	if len(pending) == 0 {
		return true, ""
	}
	summary := command
	if p.Dynamic {
		summary += "\n(contains parts that are only known at run time: command substitution, eval, or variables as commands)"
	}
	return c.ask(ctx, Request{Tool: "bash", Subjects: pending, Summary: summary}, Arity)
}

// CheckRepeat flags the third identical tool call in a row (opencode's
// "doom loop" check). Identical means same tool and byte-identical arguments.
func (c *Checker) CheckRepeat(ctx context.Context, tool, args string) (bool, string) {
	key := tool + "\x00" + args
	c.mu.Lock()
	if key == c.lastCall {
		c.repeats++
	} else {
		c.lastCall, c.repeats = key, 1
	}
	n := c.repeats
	c.mu.Unlock()
	if n < 3 {
		return true, ""
	}
	req := Request{Tool: tool, Subjects: []string{args},
		Summary: fmt.Sprintf("The agent is calling %s with identical arguments for the %s time in a row: %s", tool, ordinal(n), args)}
	ok, note := c.ask(ctx, req, nil)
	if !ok && note == "" {
		note = "You have made the same call three times in a row. Stop and change your approach."
	}
	return ok, note
}

// Escalate asks whether a command blocked by the sandbox may run without it.
func (c *Checker) Escalate(ctx context.Context, command string) bool {
	ok, _ := c.ask(ctx, Request{Tool: "bash", Subjects: []string{command},
		Summary: "The sandbox blocked this command. Run it again WITHOUT the sandbox?\n" + command}, nil)
	return ok
}

func (c *Checker) ask(ctx context.Context, req Request, pattern func(string) string) (bool, string) {
	c.mu.Lock()
	ap := c.approver
	c.mu.Unlock()
	if req.Summary == "" {
		req.Summary = strings.Join(req.Subjects, "\n")
	}
	if ap == nil {
		return false, "Permission required, but there is no one to ask (headless run). Add an allow rule or choose a different approach."
	}
	d := ap.Approve(ctx, req)
	if d.Action != Allow {
		note := "The user denied this."
		if d.Feedback != "" {
			note += " Their feedback: " + d.Feedback
		}
		return false, note
	}
	if d.Remember && pattern != nil {
		c.mu.Lock()
		for _, s := range req.Subjects {
			c.session = append(c.session, Rule{Allow, req.Tool, pattern(s)})
		}
		c.mu.Unlock()
	}
	return true, ""
}

func (c *Checker) denyNote(rules []Rule, req Request) string {
	for _, s := range req.Subjects {
		if r, ok := MatchingRule(rules, req.Tool, s); ok && r.Action == Deny {
			return fmt.Sprintf("Denied by rule %q for %s. Do not retry; choose a different approach.", r.String(), s)
		}
	}
	return fmt.Sprintf("Denied: %s is not allowed in %s mode.", req.Tool, c.Mode())
}

func ordinal(n int) string {
	switch n {
	case 3:
		return "third"
	case 4:
		return "fourth"
	}
	return fmt.Sprintf("%dth", n)
}

var twoWord = map[string]bool{"git": true, "go": true, "cargo": true, "docker": true, "kubectl": true, "gh": true,
	"brew": true, "apt": true, "pip": true, "pip3": true, "uv": true, "bun": true, "deno": true}

var scriptRunners = map[string]bool{"python": true, "python3": true, "node": true, "ruby": true, "bash": true, "sh": true, "perl": true}

// Arity turns a command into the pattern an "always allow" answer stores:
// enough leading words to name what it does, then " *" (opencode's
// permission/arity.ts idea). "git commit -m x" → "git commit *".
func Arity(cmd string) string {
	f := strings.Fields(cmd)
	n := 1
	switch {
	case len(f) == 0:
		return cmd
	case (f[0] == "npm" || f[0] == "yarn" || f[0] == "pnpm") && len(f) > 2 && (f[1] == "run" || f[1] == "exec"):
		n = 3
	case twoWord[f[0]] || f[0] == "npm" || f[0] == "yarn" || f[0] == "pnpm" || scriptRunners[f[0]]:
		n = 2
	}
	n = min(n, len(f))
	return strings.Join(f[:n], " ") + " *"
}
