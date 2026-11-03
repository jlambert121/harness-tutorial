package trace

import (
	"fmt"
	"html/template"
	"io"
	"time"

	"harness/internal/agent"
	"harness/internal/llm"
)

var page = template.Must(template.New("trace").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Agent trace</title>
<style>
:root{--bg:#fff;--fg:#1d1d1f;--muted:#6e6e73;--grid:#e5e5ea;--wait:#c7d7fe;--stream:#3a6ff7;--tool:#16a34a;--err:#dc2626}
@media (prefers-color-scheme:dark){:root{--bg:#111;--fg:#f2f2f7;--muted:#98989d;--grid:#2c2c2e;--wait:#2b3a67;--stream:#5b8cff;--tool:#22c55e;--err:#f87171}}
body{font:13px/1.4 ui-monospace,Menlo,monospace;background:var(--bg);color:var(--fg);margin:24px}
h1{font-size:16px} .sum{color:var(--muted);margin-bottom:16px}
.row{display:flex;align-items:center;height:22px;border-bottom:1px solid var(--grid)}
.label{width:320px;flex:none;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;padding-right:8px}
.track{position:relative;flex:1;height:14px}
.bar{position:absolute;top:0;height:14px;border-radius:2px;min-width:2px}
.wait{background:var(--wait)} .stream{background:var(--stream)} .tool{background:var(--tool)} .err{background:var(--err)}
.dur{width:90px;flex:none;text-align:right;color:var(--muted)}
.legend span{display:inline-block;width:12px;height:12px;margin:0 4px 0 12px;vertical-align:middle;border-radius:2px}
</style></head><body>
<h1>Agent trace</h1>
<div class="sum">{{.Summary}}</div>
{{range .Rows}}<div class="row" title="{{.Title}}"><div class="label">{{.Label}}</div><div class="track">
{{range .Bars}}<div class="bar {{.Class}}" style="left:{{.Left}}%;width:{{.Width}}%"></div>{{end}}
</div><div class="dur">{{.Dur}}</div></div>
{{end}}
<p class="legend"><span class="wait"></span>waiting for first token<span class="stream"></span>streaming<span class="tool"></span>tool<span class="err"></span>error</p>
</body></html>`))

type bar struct {
	Class       string
	Left, Width float64
}

type row struct {
	Label, Title, Dur string
	Bars              []bar
}

// RenderHTML writes a self-contained timeline page.
func RenderHTML(events []agent.Event, w io.Writer) error {
	spans := Spans(events)
	var total time.Duration
	for _, s := range spans {
		total = max(total, s.End)
	}
	if total <= 0 {
		total = 1
	}
	pct := func(d time.Duration) float64 { return float64(d) / float64(total) * 100 }
	var rows []row
	var modelTime, toolTime time.Duration
	for _, s := range spans {
		r := row{Label: s.Label, Title: s.Detail, Dur: (s.End - s.Start).Round(time.Millisecond).String()}
		switch {
		case s.IsError:
			r.Bars = []bar{{"err", pct(s.Start), pct(s.End - s.Start)}}
		case s.Kind == "tool":
			toolTime += s.End - s.Start
			r.Bars = []bar{{"tool", pct(s.Start), pct(s.End - s.Start)}}
		default:
			modelTime += s.End - s.Start
			ft := s.FirstToken
			if ft == 0 {
				ft = s.End
			}
			r.Bars = []bar{{"wait", pct(s.Start), pct(ft - s.Start)}, {"stream", pct(ft), pct(s.End - ft)}}
		}
		rows = append(rows, r)
	}
	var usage llm.Usage
	for _, e := range events {
		if e.Kind == agent.MessageEnd && e.Usage != nil {
			usage.Add(*e.Usage)
		}
	}
	summary := fmt.Sprintf("total %s · model %s · tools %s · input %d tokens (cached %d) · output %d · $%.5f",
		total.Round(time.Millisecond), modelTime.Round(time.Millisecond), toolTime.Round(time.Millisecond),
		usage.Input, usage.CacheRead, usage.Output, usage.CostUSD)
	return page.Execute(w, map[string]any{"Rows": rows, "Summary": summary})
}
