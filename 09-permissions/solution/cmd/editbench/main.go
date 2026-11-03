// Command editbench measures how each matching strategy handles the ways a
// model's old_string drifts from the file: whitespace, indentation, Unicode
// punctuation, line endings, ambiguity, and near misses that must not match.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"harness/internal/tools"
)

const base = `package server

import (
	"errors"
	"net/http"
)

// Handler serves the "status" endpoint.
type Handler struct {
	Name    string
	Retries int
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Name == "" {
		http.Error(w, "unnamed handler", http.StatusInternalServerError)
		return
	}
	w.Write([]byte("ok: " + h.Name))
}

func (h *Handler) retry(fn func() error) error {
	var err error
	for i := 0; i < h.Retries; i++ {
		if err = fn(); err == nil {
			return nil
		}
	}
	return errors.New("gave up — too many retries")
}
`

type tcase struct {
	category      string
	content, old  string
	repl          string
	shouldApply   bool
	wantSubstring string // present in the result when applied correctly
}

func cases() []tcase {
	var cs []tcase
	add := func(cat, content, old, repl string, apply bool) {
		want := ""
		if apply {
			want = strings.TrimSpace(strings.Split(repl, "\n")[0])
		}
		cs = append(cs, tcase{cat, content, old, repl, apply, want})
	}
	blocks := []string{
		"\tif r.Method != http.MethodGet {\n\t\thttp.Error(w, \"method not allowed\", http.StatusMethodNotAllowed)",
		"\tw.Write([]byte(\"ok: \" + h.Name))",
		"\tfor i := 0; i < h.Retries; i++ {\n\t\tif err = fn(); err == nil {",
		"type Handler struct {\n\tName    string\n\tRetries int\n}",
		"\tif h.Name == \"\" {\n\t\thttp.Error(w, \"unnamed handler\", http.StatusInternalServerError)",
		"// Handler serves the \"status\" endpoint.",
		"\treturn errors.New(\"gave up — too many retries\")",
		"import (\n\t\"errors\"\n\t\"net/http\"\n)",
	}
	marker := "\t// EDITED\n"
	for _, b := range blocks {
		add("exact", base, b, marker+b, true)
	}
	for _, b := range blocks[:6] {
		add("trailing whitespace", base, strings.ReplaceAll(b, "\n", "  \n")+" ", marker+b, true)
	}
	for _, b := range blocks[:6] {
		add("indentation (tabs→spaces)", base, strings.ReplaceAll(b, "\t", "    "), marker+b, true)
	}
	for _, b := range []string{blocks[1], blocks[4], blocks[5], blocks[6]} {
		smart := strings.NewReplacer(`"`, "“", "—", "-").Replace(b)
		if smart == b {
			continue
		}
		add("unicode punctuation", base, smart, marker+b, true)
	}
	add("unicode punctuation", base, "\treturn errors.New(\"gave up -- too many retries\")", "\treturn nil", false)
	crlf := strings.ReplaceAll(base, "\n", "\r\n")
	for _, b := range blocks[:4] {
		add("CRLF file, LF old_string", crlf, b, marker+b, true)
	}
	add("ambiguous", base, "\t\treturn\n\t}", "\t\treturn // x\n\t}", false)
	add("ambiguous", base, "http.Error(w, ", "writeError(w, ", false)
	add("ambiguous", base, "func (h *Handler) ", "func (h Handler) ", false)
	add("ambiguous under fuzzy", base, "    return\n  }", "return // y\n}", false)
	add("near miss (must not match)", base, "\tif r.Method != http.MethodPost {", "x", false)
	add("near miss (must not match)", base, "\tw.Write([]byte(\"ok: \" + h.ID))", "x", false)
	add("near miss (must not match)", base, "\tfor i := 0; i <= h.Retries; i++ {", "x", false)
	add("near miss (must not match)", base, "type Handler struct {\n\tName string\n\tTimeout int\n}", "x", false)
	add("near miss (must not match)", base, "\tif h.Name != \"\" {", "x", false)
	add("near miss (must not match)", base, "\treturn errors.New(\"gave up\")", "x", false)
	return cs
}

// apply mirrors the edit tool: normalize CRLF to LF, match, replace.
func apply(c tcase, strategies []tools.Strategy) (string, error) {
	body := strings.ReplaceAll(c.content, "\r\n", "\n")
	old := strings.ReplaceAll(c.old, "\r\n", "\n")
	s, e, _, err := tools.FindMatch(body, old, strategies)
	if err != nil {
		return "", err
	}
	return body[:s] + c.repl + body[e:], nil
}

func main() {
	all := cases()
	configs := []struct {
		name       string
		strategies []tools.Strategy
	}{
		{"exact only (Claude Code)", tools.AllStrategies[:1]},
		{"+ trailing_ws", tools.AllStrategies[:2]},
		{"+ trimmed", tools.AllStrategies[:3]},
		{"+ unicode (Codex order)", tools.AllStrategies},
	}
	fmt.Printf("%d edit cases\n\n", len(all))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "CATEGORY\tN")
	for _, c := range configs {
		fmt.Fprintf(tw, "\t%s", c.name)
	}
	fmt.Fprintln(tw)

	var cats []string
	byCat := map[string][]tcase{}
	for _, c := range all {
		if _, ok := byCat[c.category]; !ok {
			cats = append(cats, c.category)
		}
		byCat[c.category] = append(byCat[c.category], c)
	}
	totals := make([]struct{ ok, wrong int }, len(configs))
	for _, cat := range cats {
		fmt.Fprintf(tw, "%s\t%d", cat, len(byCat[cat]))
		for i, cfg := range configs {
			ok, wrong := 0, 0
			for _, c := range byCat[cat] {
				out, err := apply(c, cfg.strategies)
				switch {
				case !c.shouldApply && err != nil:
					ok++
				case c.shouldApply && err == nil && strings.Contains(out, c.wantSubstring) && strings.Contains(out, "// EDITED"):
					ok++
				case err == nil:
					wrong++
				default:
					var amb *tools.AmbiguousError
					_ = errors.As(err, &amb)
				}
			}
			totals[i].ok += ok
			totals[i].wrong += wrong
			cell := fmt.Sprintf("%d/%d", ok, len(byCat[cat]))
			if wrong > 0 {
				cell += fmt.Sprintf(" (%d WRONG)", wrong)
			}
			fmt.Fprintf(tw, "\t%s", cell)
		}
		fmt.Fprintln(tw)
	}
	fmt.Fprintf(tw, "TOTAL\t%d", len(all))
	for _, t := range totals {
		fmt.Fprintf(tw, "\t%d/%d", t.ok, len(all))
	}
	fmt.Fprintln(tw)
	tw.Flush()

	fmt.Println("\ncorrect outcomes (applied when it should, refused when it should)")
	for i, cfg := range configs {
		n := totals[i].ok * 40 / len(all)
		fmt.Printf("  %-26s %s %d%%\n", cfg.name, strings.Repeat("█", n)+strings.Repeat(" ", 40-n), totals[i].ok*100/len(all))
	}
	fmt.Println("\nWRONG = an edit was applied where it should have been refused (silent corruption).")
}
