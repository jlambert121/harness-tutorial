// Package hooks runs user-configured shell commands at fixed points in the
// agent's life, Claude Code style: JSON on stdin, exit 2 to block, JSON on
// stdout to rewrite arguments or add context.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type Event string

const (
	SessionStart     Event = "session_start"
	UserPromptSubmit Event = "user_prompt_submit"
	PreToolUse       Event = "pre_tool_use"
	PostToolUse      Event = "post_tool_use"
	Stop             Event = "stop"
)

var known = map[Event]bool{SessionStart: true, UserPromptSubmit: true, PreToolUse: true, PostToolUse: true, Stop: true}

type Hook struct {
	Event      Event  `json:"event"`
	Matcher    string `json:"matcher,omitempty"` // tool name, "a|b", or a regex; empty matches all
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"` // default 60
}

// Load reads {"hooks": [...]} from each existing file, in order.
func Load(paths ...string) ([]Hook, error) {
	var out []Hook
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var f struct{ Hooks []Hook }
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		for _, h := range f.Hooks {
			if !known[h.Event] {
				return nil, fmt.Errorf("%s: unknown hook event %q", p, h.Event)
			}
		}
		out = append(out, f.Hooks...)
	}
	return out, nil
}

var plainName = regexp.MustCompile(`^[\w-]+(\|[\w-]+)*$`)

// Match: "" and "*" match everything; names and "a|b" lists match exactly;
// anything else is an unanchored regular expression (Claude Code's rule).
func Match(matcher, tool string) bool {
	switch {
	case matcher == "" || matcher == "*":
		return true
	case plainName.MatchString(matcher):
		for _, m := range strings.Split(matcher, "|") {
			if m == tool {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(matcher)
	return err == nil && re.MatchString(tool)
}

type Outcome struct {
	Block             bool
	Reason            string          // why it was blocked (shown to the model)
	UpdatedArgs       json.RawMessage // pre_tool_use: replacement tool arguments
	AdditionalContext string          // text for the model
	Error             string          // hook failures that did not block
}

type Runner struct {
	root  string
	hooks []Hook
}

func New(root string, hooks []Hook) *Runner { return &Runner{root: root, hooks: hooks} }

// Run executes every hook for event whose matcher accepts tool. payload is
// sent as JSON on stdin with event, tool, and cwd added.
func (r *Runner) Run(ctx context.Context, event Event, tool string, payload map[string]any) Outcome {
	var out Outcome
	if r == nil {
		return out
	}
	in := map[string]any{}
	for k, v := range payload {
		in[k] = v
	}
	in["event"], in["tool"], in["cwd"] = string(event), tool, r.root
	stdin, _ := json.Marshal(in)
	var contexts, errs []string
	for _, h := range r.hooks {
		if h.Event != event || (tool != "" && !Match(h.Matcher, tool)) {
			continue
		}
		res := r.exec(ctx, h, stdin)
		if res.Block {
			out.Block = true
			if out.Reason == "" {
				out.Reason = res.Reason
			}
		}
		if len(res.UpdatedArgs) > 0 {
			out.UpdatedArgs = res.UpdatedArgs
		}
		if res.AdditionalContext != "" {
			contexts = append(contexts, res.AdditionalContext)
		}
		if res.Error != "" {
			errs = append(errs, res.Error)
		}
	}
	out.AdditionalContext = strings.Join(contexts, "\n")
	out.Error = strings.Join(errs, "\n")
	return out
}

type jsonOut struct {
	Decision          string          `json:"decision"`
	Reason            string          `json:"reason"`
	UpdatedArgs       json.RawMessage `json:"updated_args"`
	AdditionalContext string          `json:"additional_context"`
}

func (r *Runner) exec(ctx context.Context, h Hook, stdin []byte) Outcome {
	timeout := time.Duration(h.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", h.Command)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "HARNESS_EVENT="+string(h.Event), "HARNESS_ROOT="+r.root)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return Outcome{Error: fmt.Sprintf("hook %q timed out after %s", h.Command, timeout)}
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		return Outcome{Error: fmt.Sprintf("hook %q: %v", h.Command, err)}
	}
	switch code {
	case 0:
		text := strings.TrimSpace(stdout.String())
		var j jsonOut
		if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &j) == nil {
			return Outcome{Block: j.Decision == "block", Reason: j.Reason, UpdatedArgs: j.UpdatedArgs, AdditionalContext: j.AdditionalContext}
		}
		if h.Event == SessionStart || h.Event == UserPromptSubmit {
			return Outcome{AdditionalContext: text}
		}
		return Outcome{}
	case 2:
		return Outcome{Block: true, Reason: strings.TrimSpace(stderr.String())}
	}
	return Outcome{Error: fmt.Sprintf("hook %q exited %d: %s", h.Command, code, strings.TrimSpace(stderr.String()))}
}
