package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/permission"
	"harness/internal/tools"
)

type recorder struct {
	reqs     []permission.Request
	decision permission.Decision
}

func (r *recorder) Approve(ctx context.Context, req permission.Request) permission.Decision {
	r.reqs = append(r.reqs, req)
	return r.decision
}

func permWorkspace(t *testing.T, mode permission.Mode, rules []string, d permission.Decision) (*tools.Env, *recorder, func(string, map[string]any) string) {
	t.Helper()
	env, r := editWorkspace(t, map[string]string{"a.go": src, ".env": "SECRET=1\n"})
	r.Register(tools.Bash())
	var rs []permission.Rule
	for _, s := range rules {
		rule, err := permission.ParseRule(s)
		if err != nil {
			t.Fatal(err)
		}
		rs = append(rs, rule)
	}
	ap := &recorder{decision: d}
	env.Perm = permission.NewChecker(mode, append(permission.DefaultRules(), rs...), ap)
	return env, ap, func(name string, args map[string]any) string {
		res := call(t, r, name, args)
		if res.IsError {
			return "ERR " + res.Content
		}
		return res.Content
	}
}

func TestPlanModeDeniesEdits(t *testing.T) {
	env, ap, run := permWorkspace(t, permission.ModePlan, nil, permission.Decision{})
	run("read", map[string]any{"path": "a.go"})
	out := run("edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	if !strings.HasPrefix(out, "ERR Permission denied") || len(ap.reqs) != 0 {
		t.Errorf("out=%q asked=%d", out, len(ap.reqs))
	}
	if readFile(t, env, "a.go") != src {
		t.Error("file changed in plan mode")
	}
	if out := run("bash", map[string]any{"command": "touch made-in-plan-mode"}); !strings.HasPrefix(out, "ERR Permission denied") {
		t.Errorf("bash in plan mode = %q", out)
	}
	if out := run("bash", map[string]any{"command": "ls"}); strings.HasPrefix(out, "ERR") {
		t.Errorf("read-only bash should work in plan mode: %q", out)
	}
}

func TestDefaultModeAsksForEditsAndPassesFeedback(t *testing.T) {
	env, ap, run := permWorkspace(t, permission.ModeDefault, nil, permission.Decision{Action: permission.Deny, Feedback: "use a constant instead"})
	run("read", map[string]any{"path": "a.go"})
	out := run("edit", map[string]any{"path": "a.go", "old_string": "x := 1", "new_string": "x := 2"})
	if len(ap.reqs) != 1 || ap.reqs[0].Tool != "edit" || ap.reqs[0].Subjects[0] != "a.go" {
		t.Errorf("asked = %+v", ap.reqs)
	}
	if !strings.Contains(out, "use a constant instead") || readFile(t, env, "a.go") != src {
		t.Errorf("denial feedback must reach the model: %q", out)
	}
}

func TestBashDenyRuleExplainsItself(t *testing.T) {
	_, ap, run := permWorkspace(t, permission.ModeYolo, []string{"deny bash(git push*)"}, permission.Decision{})
	out := run("bash", map[string]any{"command": "git status && git push origin main"})
	if !strings.Contains(out, "deny bash(git push*)") || len(ap.reqs) != 0 {
		t.Errorf("out=%q", out)
	}
}

func TestReadingDotEnvAsks(t *testing.T) {
	_, ap, run := permWorkspace(t, permission.ModeYolo, nil, permission.Decision{Action: permission.Deny})
	out := run("read", map[string]any{"path": ".env"})
	if !strings.HasPrefix(out, "ERR Permission denied") || len(ap.reqs) != 1 {
		t.Errorf("reading .env should ask even in yolo mode (an ask rule): %q", out)
	}
}

func TestRedirectAsksAsWrite(t *testing.T) {
	_, ap, run := permWorkspace(t, permission.ModeDefault, []string{"allow bash(echo *)"}, permission.Decision{Action: permission.Deny})
	run("bash", map[string]any{"command": "echo pwned >> ~/.ssh/authorized_keys"})
	if len(ap.reqs) != 1 || !strings.Contains(strings.Join(ap.reqs[0].Subjects, " "), "~/.ssh/authorized_keys") {
		t.Errorf("an allowed command with an output redirect must still ask about the write: %+v", ap.reqs)
	}
}

func TestRepeatedCallsAreChecked(t *testing.T) {
	_, ap, run := permWorkspace(t, permission.ModeYolo, nil, permission.Decision{Action: permission.Deny})
	for i := 0; i < 3; i++ {
		run("ls", map[string]any{})
	}
	if len(ap.reqs) != 1 || !strings.Contains(ap.reqs[0].Summary, "identical arguments") {
		t.Errorf("third identical call should ask: %+v", ap.reqs)
	}
}

func TestBashSandboxBlocksAndEscalates(t *testing.T) {
	if !permission.SandboxAvailable() {
		t.Skip("Seatbelt is macOS-only")
	}
	env, ap, run := permWorkspace(t, permission.ModeYolo, nil, permission.Decision{Action: permission.Deny})
	outside := t.TempDir()
	env.Sandbox = &permission.Sandbox{Writable: []string{env.Root}}
	target := filepath.Join(outside, "x.txt")
	out := run("bash", map[string]any{"command": "touch " + target})
	if _, err := os.Stat(target); err == nil {
		t.Fatal("sandbox let a write escape")
	}
	if len(ap.reqs) != 1 || !strings.Contains(ap.reqs[0].Summary, "WITHOUT the sandbox") || !strings.Contains(out, "sandbox blocked") {
		t.Errorf("asked=%+v out=%q", ap.reqs, out)
	}
	ap.decision = permission.Decision{Action: permission.Allow}
	out = run("bash", map[string]any{"command": "touch " + target})
	if _, err := os.Stat(target); err != nil || !strings.Contains(out, "approved by the user") {
		t.Errorf("approved escalation should rerun unsandboxed: %q", out)
	}
	if out := run("bash", map[string]any{"command": "touch inside.txt"}); strings.HasPrefix(out, "ERR") {
		t.Errorf("writes inside the workspace must work under the sandbox: %q", out)
	}
}
