// Command models lists Vercel AI Gateway models usable for a coding agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"harness/internal/llm"
)

func main() {
	base := flag.String("base", "https://ai-gateway.vercel.sh/v1", "gateway base URL")
	tag := flag.String("tag", "tool-use", "only models with this tag (empty = all)")
	sortBy := flag.String("sort", "price", "price | context | id")
	limit := flag.Int("limit", 25, "max rows (0 = all)")
	all := flag.Bool("all", false, "include models your team cannot use")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := os.Getenv("AI_GATEWAY_API_KEY")
	models, err := llm.FetchCatalog(ctx, *base, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var rows []llm.ModelInfo
	for _, m := range models {
		if m.Type != "language" || (*tag != "" && !m.Has(*tag)) {
			continue
		}
		if !*all && m.Eligibility == "ineligible" {
			continue
		}
		rows = append(rows, m)
	}
	slices.SortFunc(rows, func(a, b llm.ModelInfo) int {
		switch *sortBy {
		case "context":
			return b.ContextWindow - a.ContextWindow
		case "id":
			return strings.Compare(a.ID, b.ID)
		default:
			return cmpFloat(blended(a), blended(b))
		}
	})
	total := len(rows)
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tCONTEXT\t$IN/M\t$OUT/M\t$CACHE-READ/M\tCACHING\tREASONING\tUSABLE")
	for _, m := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%.3f\t%.3f\t%s\t%s\t%s\t%s\n",
			m.ID, human(m.ContextWindow), m.Pricing.Input*1e6, m.Pricing.Output*1e6,
			cacheRead(m), caching(m), yes(m.Has("reasoning")), usable(m))
	}
	tw.Flush()
	fmt.Printf("\n%d of %d matching models shown (%d in catalog).", len(rows), total, len(models))
	if key == "" {
		fmt.Print(" Set AI_GATEWAY_API_KEY to see which models your team may use.")
	}
	fmt.Println()
}

// blended approximates an agent workload: input-heavy, roughly 10:1 input to output tokens.
func blended(m llm.ModelInfo) float64 { return 10*m.Pricing.Input + m.Pricing.Output }

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func human(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2gM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

func cacheRead(m llm.ModelInfo) string {
	if m.Pricing.CacheRead == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f", m.Pricing.CacheRead*1e6)
}

func caching(m llm.ModelInfo) string {
	var c []string
	if m.Has("implicit-caching") {
		c = append(c, "implicit")
	}
	if m.Has("explicit-caching") {
		c = append(c, "explicit")
	}
	if len(c) == 0 {
		return "none"
	}
	return strings.Join(c, "+")
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "-"
}

func usable(m llm.ModelInfo) string {
	switch m.Eligibility {
	case "eligible":
		return "yes"
	case "ineligible":
		return "no (" + m.IneligibleReason + ")"
	}
	return "?"
}
