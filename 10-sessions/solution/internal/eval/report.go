package eval

import (
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// PrintSummary writes the per-config table, the per-task matrix, and bars.
func PrintSummary(w io.Writer, outs []Outcome) {
	sums := Summarize(outs)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONFIG\tPASS\tRATE ± SD\tMEAN COST\tMEAN TURNS\tMEAN TIME\tERRORS")
	for _, s := range sums {
		fmt.Fprintf(tw, "%s\t%d/%d\t%.0f%% ± %.0f\t$%.4f\t%.1f\t%s\t%d\n", s.Config, s.Passes, s.Runs,
			s.PassRate*100, s.PassStd*100, s.MeanCost, s.MeanTurns, s.MeanWall.Round(time.Second), s.Errors)
	}
	tw.Flush()

	fmt.Fprintln(w, "\npass rate")
	for _, s := range sums {
		n := int(s.PassRate * 40)
		fmt.Fprintf(w, "  %-28s %s %3.0f%%\n", s.Config, strings.Repeat("█", n)+strings.Repeat("·", 40-n), s.PassRate*100)
	}

	byTask := ByTask(outs)
	var names []string
	for t := range byTask {
		names = append(names, t)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "\nper task (pass rate over repeats)")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "TASK")
	for _, s := range sums {
		fmt.Fprintf(tw, "\t%s", s.Config)
	}
	fmt.Fprintln(tw)
	for _, t := range names {
		fmt.Fprint(tw, t)
		for _, s := range sums {
			fmt.Fprintf(tw, "\t%s", cell(byTask[t][s.Config]))
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
}

func cell(rate float64) string {
	switch {
	case rate == 1:
		return "███ 100%"
	case rate == 0:
		return "··· 0%"
	}
	return fmt.Sprintf("%s %.0f%%", strings.Repeat("█", int(rate*3+0.5))+strings.Repeat("·", 3-int(rate*3+0.5)), rate*100)
}

var reportTmpl = template.Must(template.New("r").Funcs(template.FuncMap{
	"pct": func(f float64) string { return fmt.Sprintf("%.0f", f*100) },
	"usd": func(f float64) string { return fmt.Sprintf("$%.4f", f) },
}).Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Eval report</title>
<style>
:root{--bg:#fff;--fg:#1d1d1f;--muted:#6e6e73;--grid:#e5e5ea;--bar:#3a6ff7;--pass:#16a34a;--fail:#e5e5ea}
@media (prefers-color-scheme:dark){:root{--bg:#111;--fg:#f2f2f7;--muted:#98989d;--grid:#2c2c2e;--bar:#5b8cff;--pass:#22c55e;--fail:#2c2c2e}}
body{font:14px/1.45 -apple-system,system-ui,sans-serif;background:var(--bg);color:var(--fg);margin:24px;max-width:960px}
h1{font-size:20px}h2{font-size:15px;margin-top:28px}td,th{padding:4px 10px;text-align:left;border-bottom:1px solid var(--grid)}
.num{text-align:right;font-variant-numeric:tabular-nums}.muted{color:var(--muted)}
.bar{position:relative;height:16px;background:var(--grid);border-radius:3px;width:320px}
.fill{position:absolute;left:0;top:0;bottom:0;background:var(--bar);border-radius:3px}
.err{position:absolute;top:6px;height:4px;border-left:2px solid var(--fg);border-right:2px solid var(--fg)}
.dot{display:inline-block;width:14px;height:14px;border-radius:3px;margin-right:2px}
.p{background:var(--pass)}.f{background:var(--fail)}
</style></head><body>
<h1>Eval report</h1><p class="muted">{{.Runs}} runs · {{.Tasks}} tasks</p>
<h2>Pass rate (bar = mean, whisker = ±1 SD across repeats)</h2>
<table>{{range .Sums}}<tr><td>{{.Config}}</td><td><div class="bar"><div class="fill" style="width:{{pct .PassRate}}%"></div>
<div class="err" style="left:{{.Lo}}%;width:{{.Span}}%"></div></div></td><td class="num">{{pct .PassRate}}%</td>
<td class="num">{{usd .MeanCost}}/run</td><td class="num">{{printf "%.1f" .MeanTurns}} turns</td></tr>{{end}}</table>
<h2>Per task (one square per run: green = pass)</h2>
<table><tr><th>task</th>{{range .Sums}}<th>{{.Config}}</th>{{end}}</tr>
{{range .Rows}}<tr><td>{{.Task}}</td>{{range .Cells}}<td>{{range .}}<span class="dot {{if .}}p{{else}}f{{end}}"></span>{{end}}</td>{{end}}</tr>{{end}}</table>
</body></html>`))

type htmlSum struct {
	Summary
	Lo, Span float64
}

type htmlRow struct {
	Task  string
	Cells [][]bool
}

// WriteHTML writes a self-contained report page.
func WriteHTML(w io.Writer, outs []Outcome) error {
	sums := Summarize(outs)
	var hs []htmlSum
	for _, s := range sums {
		lo := max(0, s.PassRate-s.PassStd) * 100
		hi := min(1, s.PassRate+s.PassStd) * 100
		hs = append(hs, htmlSum{s, lo, hi - lo})
	}
	tasks := map[string]bool{}
	for _, o := range outs {
		tasks[o.Task] = true
	}
	var names []string
	for t := range tasks {
		names = append(names, t)
	}
	sort.Strings(names)
	var rows []htmlRow
	for _, t := range names {
		r := htmlRow{Task: t}
		for _, s := range sums {
			var cells []bool
			for _, o := range outs {
				if o.Task == t && o.Config == s.Config {
					cells = append(cells, o.Pass)
				}
			}
			r.Cells = append(r.Cells, cells)
		}
		rows = append(rows, r)
	}
	return reportTmpl.Execute(w, map[string]any{"Sums": hs, "Rows": rows, "Runs": len(outs), "Tasks": len(names)})
}
