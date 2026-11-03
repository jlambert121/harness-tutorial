// Command ctxsim simulates a long session offline (scripted model, real read
// tool) and charts the size of every request with and without pruning.
// With -live it also measures how far bytes/4 is from real token counts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/agent"
	"harness/internal/ctxmgr"
	"harness/internal/llm"
	"harness/internal/llm/fake"
	"harness/internal/tools"
)

const prompts = 30

func main() {
	window := flag.Int("window", 200000, "simulated context window (tokens)")
	fileKB := flag.Int("file-kb", 24, "size of each file the model reads")
	htmlOut := flag.String("html", "ctxsim.html", "chart output")
	live := flag.Bool("live", false, "also measure bytes/4 against a real tokenizer (Haiku, about $0.002)")
	flag.Parse()

	root, _ := os.MkdirTemp("", "ctxsim")
	defer os.RemoveAll(root)
	for i := 1; i <= prompts; i++ {
		line := fmt.Sprintf("func handler%03d(w http.ResponseWriter, r *http.Request) { /* generated */ }\n", i)
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%02d.go", i)), []byte(strings.Repeat(line, *fileKB*1024/len(line))), 0o644)
	}
	budget := ctxmgr.Budget{Window: *window, Reserve: 20000}

	series := map[string][]int{}
	for _, mode := range []string{"no pruning", "pruning"} {
		series[mode] = simulate(root, budget, mode == "pruning")
	}

	usable := budget.Usable()
	fmt.Printf("%d prompts, each reading one %d KB file, window %d (usable %d)\n\n", prompts, *fileKB, *window, usable)
	for _, mode := range []string{"no pruning", "pruning"} {
		s := series[mode]
		over := -1
		for i, v := range s {
			if v > usable && over < 0 {
				over = i + 1
			}
		}
		fmt.Printf("%-11s %s  last request %6d tokens", mode, spark(s, usable), s[len(s)-1])
		if over > 0 {
			fmt.Printf("  ✗ exceeds usable context at request %d", over)
		}
		fmt.Println()
	}
	fmt.Println("            (each column is one request; █ = usable context)")
	writeHTML(*htmlOut, series, usable)
	fmt.Println("\nwrote", *htmlOut)

	if *live {
		calibrate()
	}
}

// simulate runs the agent against a scripted model that reads one new file
// per prompt, and returns the estimated input tokens of every request.
func simulate(root string, budget ctxmgr.Budget, prune bool) []int {
	t := &testing.T{}
	var turns []fake.Turn
	for i := 1; i <= prompts; i++ {
		turns = append(turns, fake.Tools(fake.Call(fmt.Sprintf("c%d", i), "read", map[string]string{"path": fmt.Sprintf("file%02d.go", i)})),
			fake.Text(fmt.Sprintf("file%02d defines one generated handler.", i)))
	}
	srv := fake.Start(t, turns...)
	reg := tools.NewRegistry(&tools.Env{Root: root})
	reg.Register(tools.Read())
	cfg := agent.Config{Client: llm.NewGateway(srv.URL, "k"), Model: "sim", System: "You summarize files.", Tools: reg}
	var sizes []int
	cal := &ctxmgr.Calibrator{}
	if prune {
		m := ctxmgr.NewManager(budget, 0.7, 40000)
		cfg.Transform = m.Transform
	}
	a := agent.New(cfg)
	for i := 1; i <= prompts; i++ {
		a.Run(context.Background(), fmt.Sprintf("Read file%02d.go and summarize it.", i), nil)
	}
	for _, body := range srv.Requests() {
		sizes = append(sizes, cal.Estimate(string(body)))
	}
	return sizes
}

func spark(s []int, limit int) string {
	bars := []rune(" ▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range s {
		i := v * 8 / limit
		if i > 8 {
			b.WriteRune('✗')
			continue
		}
		b.WriteRune(bars[i])
	}
	return b.String()
}

var chart = template.Must(template.New("c").Parse(`<!doctype html><meta charset="utf-8"><title>Context per request</title>
<style>:root{--bg:#fff;--fg:#1d1d1f;--grid:#e5e5ea;--a:#dc2626;--b:#2563eb}
@media (prefers-color-scheme:dark){:root{--bg:#111;--fg:#f2f2f7;--grid:#2c2c2e;--a:#f87171;--b:#60a5fa}}
body{font:14px system-ui;background:var(--bg);color:var(--fg);margin:24px}</style>
<h1 style="font-size:18px">Input tokens per request</h1>
<svg viewBox="0 0 {{.W}} {{.H}}" width="100%" style="max-width:900px">
<line x1="40" x2="{{.W}}" y1="{{.LimitY}}" y2="{{.LimitY}}" stroke="var(--fg)" stroke-dasharray="4 4"/>
<text x="44" y="{{.LimitY}}" dy="-4" fill="var(--fg)" font-size="12">usable context ({{.Limit}})</text>
{{range .Lines}}<polyline fill="none" stroke="{{.Color}}" stroke-width="2" points="{{.Points}}"/>
<text x="{{$.LegendX}}" y="{{.LegendY}}" fill="{{.Color}}" font-size="12">{{.Name}}</text>{{end}}
</svg>`))

func writeHTML(path string, series map[string][]int, limit int) {
	const w, h = 900, 360
	maxV := limit
	n := 0
	for _, s := range series {
		for _, v := range s {
			maxV = max(maxV, v)
		}
		n = max(n, len(s))
	}
	y := func(v int) int { return h - 20 - v*(h-40)/maxV }
	type line struct {
		Name, Color, Points string
		LegendY             int
	}
	var lines []line
	for i, name := range []string{"no pruning", "pruning"} {
		var pts []string
		for j, v := range series[name] {
			pts = append(pts, fmt.Sprintf("%d,%d", 40+j*(w-60)/max(n-1, 1), y(v)))
		}
		lines = append(lines, line{name, []string{"var(--a)", "var(--b)"}[i], strings.Join(pts, " "), 20 + 16*i})
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	chart.Execute(f, map[string]any{"W": w, "H": h, "LimitY": y(limit), "Limit": limit, "Lines": lines, "LegendX": w - 140})
}

// calibrate sends three kinds of text to a real model and compares the
// provider's prompt_tokens with the bytes/4 estimate.
func calibrate() {
	goroot, _ := exec.Command("go", "env", "GOROOT").Output()
	src, _ := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "src", "net", "http", "server.go"))
	prose := strings.Repeat("The quick brown fox jumps over the lazy dog while the harness counts every token it sends. ", 300)
	var rows []map[string]any
	for i := 0; i < 300; i++ {
		rows = append(rows, map[string]any{"id": i, "name": fmt.Sprintf("user-%d", i), "active": i%2 == 0, "score": float64(i) * 1.5})
	}
	js, _ := json.Marshal(rows)
	client := llm.NewGateway("https://ai-gateway.vercel.sh/v1", os.Getenv("AI_GATEWAY_API_KEY"))
	fmt.Println("\nbytes/4 vs real tokens (anthropic/claude-haiku-5.5):")
	for _, c := range []struct{ name, text string }{{"Go source", string(src[:min(len(src), 40000)])}, {"English prose", prose}, {"JSON", string(js)}} {
		var u *llm.Usage
		for ev := range client.Stream(context.Background(), llm.Request{Model: "anthropic/claude-haiku-5.5", MaxTokens: 16,
			Messages: []llm.Message{llm.UserText(c.text + "\n\nReply with OK.")}}) {
			if ev.Type == llm.EventDone {
				u = ev.Message.Usage
			}
		}
		if u == nil {
			fmt.Println("  request failed")
			continue
		}
		est := (len(c.text) + 3) / 4
		fmt.Printf("  %-14s %6d bytes  estimate %6d  actual %6d  → %.2f bytes/token (ratio %.2f)\n",
			c.name, len(c.text), est, u.Input, float64(len(c.text))/float64(u.Input), float64(u.Input)/float64(est))
	}
}
