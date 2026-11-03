package main

import (
	"context"
	"encoding/json"
	"testing"

	"harness/internal/llm/fake"
)

func TestModelCommandOverridesRouter(t *testing.T) {
	t.Setenv("HARNESS_DATA_DIR", t.TempDir())
	url, requests := fake.NewServer(t, fake.Text("ok"))
	o, _, err := flags("harness", []string{"--base", url, "--root", t.TempDir(), "--no-session", "--no-memory",
		"--model", "main", "--small-model", "small", "--router", "escalate"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, ap, err := buildApp(o)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := ap.commands(a).Lookup("model")
	if _, err := cmd.Run(context.Background(), "chosen"); err != nil {
		t.Fatal(err)
	}
	a.Run(context.Background(), "hi", nil)
	var req struct{ Model string }
	json.Unmarshal(requests()[0], &req)
	if req.Model != "chosen" {
		t.Errorf("request went to %q: /model must take precedence over --router", req.Model)
	}
}
