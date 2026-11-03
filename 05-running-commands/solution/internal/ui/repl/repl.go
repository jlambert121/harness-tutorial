// Package repl is a line-based front end that renders agent events as text.
package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"harness/internal/agent"
)

const (
	dim   = "\x1b[2m"
	red   = "\x1b[31m"
	cyan  = "\x1b[36m"
	reset = "\x1b[0m"
)

// Renderer prints events as they arrive.
type Renderer struct {
	Out        io.Writer
	inThinking bool
	midLine    bool
}

func (r *Renderer) Sink(e agent.Event) {
	w := r.Out
	switch e.Kind {
	case agent.ThinkingDelta:
		if !r.inThinking {
			fmt.Fprint(w, dim)
			r.inThinking = true
		}
		fmt.Fprint(w, e.Text)
		r.midLine = !strings.HasSuffix(e.Text, "\n")
	case agent.TextDelta:
		r.endThinking()
		fmt.Fprint(w, e.Text)
		r.midLine = !strings.HasSuffix(e.Text, "\n")
	case agent.ToolStart:
		r.endThinking()
		r.newline()
		fmt.Fprintf(w, "%s→ %s %s%s\n", cyan, e.Call.Name, oneLine(string(e.Call.Args), 100), reset)
	case agent.ToolEnd:
		color := dim
		if e.Result.IsError {
			color = red
		}
		lines := strings.Count(e.Result.Content, "\n") + 1
		fmt.Fprintf(w, "%s  ← %s (%d lines)%s\n", color, oneLine(e.Result.Content, 100), lines, reset)
	case agent.Error:
		r.endThinking()
		r.newline()
		fmt.Fprintf(w, "%serror: %s%s\n", red, e.Text, reset)
	case agent.AgentEnd:
		r.endThinking()
		r.newline()
		if u := e.Usage; u != nil {
			fmt.Fprintf(w, "%s[in %d (cached %d) · out %d · $%.5f]%s\n", dim, u.Input, u.CacheRead, u.Output, u.CostUSD, reset)
		}
	}
}

func (r *Renderer) endThinking() {
	if r.inThinking {
		fmt.Fprint(r.Out, reset+"\n")
		r.inThinking, r.midLine = false, false
	}
}

func (r *Renderer) newline() {
	if r.midLine {
		fmt.Fprintln(r.Out)
		r.midLine = false
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

// Run reads prompts line by line and runs the agent on each. Ctrl-C cancels
// the current run; Ctrl-D exits. extra receives every event too (may be nil).
func Run(ctx context.Context, a *agent.Agent, in io.Reader, out io.Writer, extra agent.Sink) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	r := &Renderer{Out: out}
	if reg := a.Tools(); reg != nil {
		reg.Env().Confirm = func(ctx context.Context, command string) bool {
			r.newline()
			fmt.Fprintf(out, "%sRun this command?%s\n  %s\n[y/N] ", cyan, reset, command)
			if !sc.Scan() {
				return false
			}
			ans := strings.ToLower(strings.TrimSpace(sc.Text()))
			return ans == "y" || ans == "yes"
		}
	}
	for {
		fmt.Fprint(out, "\n> ")
		if !sc.Scan() {
			fmt.Fprintln(out)
			return sc.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		err := a.Run(runCtx, line, agent.Tee(r.Sink, extra))
		stop()
		if err != nil && runCtx.Err() != nil {
			fmt.Fprintf(out, "%s[cancelled]%s\n", dim, reset)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}
