package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/llm/fake"
)

func TestExecEmitsJSONL(t *testing.T) {
	t.Setenv("HARNESS_DATA_DIR", t.TempDir())
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	url, _ := fake.NewServer(t,
		fake.Tools(fake.Call("c1", "read", map[string]string{"path": "a.txt"})),
		fake.Text("it says hello"),
	)
	var out, errOut bytes.Buffer
	code := runExec([]string{"--json", "--base", url, "--root", root, "--model", "m", "what does a.txt say?"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var kinds []agent.EventKind
	for _, l := range lines {
		var e agent.Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("not JSONL: %q", l)
		}
		if e.Kind == agent.TextDelta {
			t.Error("--json should omit text deltas; message_end carries the text")
		}
		kinds = append(kinds, e.Kind)
	}
	if kinds[0] != agent.AgentStart || kinds[len(kinds)-1] != agent.AgentEnd {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestExecFailureExitCode(t *testing.T) {
	t.Setenv("HARNESS_DATA_DIR", t.TempDir())
	url, _ := fake.NewServer(t, fake.Turn{Status: 400, Body: `{"error":{"message":"bad"}}`})
	var out, errOut bytes.Buffer
	if code := runExec([]string{"--base", url, "--root", t.TempDir(), "x"}, &out, &errOut); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if code := runExec([]string{"--root", t.TempDir()}, &out, &errOut); code != 2 {
		t.Errorf("missing prompt: exit = %d, want 2", code)
	}
}
