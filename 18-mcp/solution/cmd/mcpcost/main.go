// Command mcpcost measures how much context an MCP server's tool
// definitions take when loaded up front versus behind tool_search, and with
// -live runs a task that has to discover and use MCP tools.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/mcp"
	"harness/internal/tools"
)

func main() {
	synthetic := flag.Int("synthetic", 40, "filler tools the server exposes besides todo_add, todo_list, word_count")
	live := flag.Bool("live", false, "run a task through the harness with the server deferred")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "-live model")
	flag.Parse()
	dir, _ := os.MkdirTemp("", "mcpcost")
	defer os.RemoveAll(dir)
	server := filepath.Join(dir, "mini-mcp")
	if out, err := exec.Command("go", "build", "-o", server, "./cmd/mini-mcp").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	argv := []string{server, "--synthetic", fmt.Sprint(*synthetic)}
	cost := func(deferred bool) (int, int) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := mcp.Start(ctx, "work", argv, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer c.Close()
		reg := tools.NewRegistry(&tools.Env{})
		mcp.Register(ctx, reg, "work", c, deferred)
		n := 0
		for _, s := range reg.Specs() {
			n += (len(s.Name) + len(s.Description) + len(s.Schema) + 3) / 4
		}
		return len(reg.Specs()), n
	}
	ln, lt := cost(false)
	dn, dt := cost(true)
	fmt.Printf("MCP server with %d tools\n", *synthetic+3)
	fmt.Printf("  loaded up front   %3d definitions  %6d tokens  %s\n", ln, lt, strings.Repeat("█", 50))
	fmt.Printf("  deferred          %3d definition   %6d tokens  %s\n", dn, dt, strings.Repeat("█", max(1, dt*50/lt)))
	fmt.Printf("  every request carries the definitions: %d tokens saved per request\n", lt-dt)

	if !*live {
		return
	}
	work := filepath.Join(dir, "work")
	os.MkdirAll(filepath.Join(work, ".harness"), 0o755)
	cfg, _ := json.Marshal(map[string]any{"servers": map[string]any{"work": map[string]any{"command": argv, "deferred": true}}})
	os.WriteFile(filepath.Join(work, ".harness", "mcp.json"), cfg, 0o644)
	trace := filepath.Join(dir, "trace.jsonl")
	prompt := "Add 'buy milk' and 'call the plumber' to the todo list, then show me the list."
	cmd := exec.Command("go", "run", "./cmd/harness", "exec", "--no-session", "--no-memory", "--root", work, "--model", *model, "--trace", trace, prompt)
	cmd.Env = append(os.Environ(), "HARNESS_DATA_DIR="+dir)
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Printf("\nlive (%s, deferred): %q\n", *model, prompt)
	f, _ := os.Open(trace)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Kind string
			Call *struct {
				Name string
				Args json.RawMessage
			}
			Result *struct{ Content string }
			Data   map[string]any
		}
		json.Unmarshal(sc.Bytes(), &e)
		switch e.Kind {
		case "tool_start":
			fmt.Printf("  → %s %s\n", e.Call.Name, e.Call.Args)
		case "tool_end":
			fmt.Printf("      %s\n", strings.ReplaceAll(strings.TrimSpace(e.Result.Content), "\n", "\n      "))
		case "context":
			fmt.Printf("  [request: ~%v tokens]\n", e.Data["estimated"])
		}
	}
}
