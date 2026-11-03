// Command bench streams the same prompt through several models and compares
// time to first token, output throughput, and cost.
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

const defaultModels = "anthropic/claude-sonnet-5.5,anthropic/claude-haiku-5.5,openai/gpt-6-luna,google/gemini-3.8-flash,deepseek/deepseek-v4.1-flash"

const prompt = "Write a Go function that parses a duration like \"1h30m\" without using time.ParseDuration. Code only, no explanation."

type run struct {
	ttft, firstText, total time.Duration
	out, reasoning         int
	reasoningStreamed      bool
	cost                   float64
	err                    string
}

func main() {
	models := flag.String("models", defaultModels, "comma-separated model ids")
	n := flag.Int("n", 3, "runs per model")
	base := flag.String("base", "https://ai-gateway.vercel.sh/v1", "gateway base URL")
	flag.Parse()

	client := llm.NewGateway(*base, os.Getenv("AI_GATEWAY_API_KEY"))
	ids := strings.Split(*models, ",")
	results := map[string][]run{}
	for i := 0; i < *n; i++ {
		for _, id := range ids {
			r := once(client, id)
			results[id] = append(results[id], r)
			status := "ok"
			if r.err != "" {
				status = r.err
			}
			fmt.Fprintf(os.Stderr, "run %d %-32s ttft %6s total %6s %s\n", i+1, id, r.ttft.Round(time.Millisecond), r.total.Round(time.Millisecond), status)
		}
	}

	type row struct {
		id                                    string
		ttft, firstText, tps, cost, reasoning float64
		streamed                              bool
		ok                                    int
	}
	var rows []row
	for _, id := range ids {
		var ttfts, texts, tpss, costs, reasons []float64
		streamed := false
		for _, r := range results[id] {
			if r.err != "" {
				continue
			}
			ttfts = append(ttfts, r.ttft.Seconds())
			texts = append(texts, r.firstText.Seconds())
			// Hidden reasoning tokens are generated before the first streamed token,
			// so they only count toward throughput when the provider streams them.
			tokens := r.out
			if !r.reasoningStreamed {
				tokens -= r.reasoning
			}
			streamed = streamed || r.reasoningStreamed
			if gen := (r.total - r.ttft).Seconds(); gen > 0 {
				tpss = append(tpss, float64(tokens)/gen)
			}
			reasons = append(reasons, float64(r.reasoning))
			costs = append(costs, r.cost)
		}
		rows = append(rows, row{id, median(ttfts), median(texts), median(tpss), mean(costs), median(reasons), streamed, len(ttfts)})
	}

	fmt.Printf("\nprompt: %q\n%d runs per model; medians for latency/throughput, mean cost\n\n", prompt, *n)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tOK\tFIRST TOKEN\tFIRST TEXT\tTOK/S\tREASONING TOK\tCOST/RUN")
	for _, r := range rows {
		vis := "hidden"
		if r.streamed {
			vis = "streamed"
		}
		fmt.Fprintf(tw, "%s\t%d/%d\t%.2fs\t%.2fs\t%.0f\t%.0f (%s)\t$%.5f\n", r.id, r.ok, *n, r.ttft, r.firstText, r.tps, r.reasoning, vis, r.cost)
	}
	tw.Flush()

	maxOf := func(f func(row) float64) float64 {
		m := 0.0
		for _, r := range rows {
			m = max(m, f(r))
		}
		return m
	}
	for _, chart := range []struct {
		title string
		f     func(row) float64
		unit  string
	}{
		{"time to first answer text (lower is better)", func(r row) float64 { return r.firstText }, "s"},
		{"streamed tokens/second (higher is better)", func(r row) float64 { return r.tps }, ""},
		{"cost per run (lower is better)", func(r row) float64 { return r.cost * 1000 }, " m$"},
	} {
		fmt.Printf("\n%s\n", chart.title)
		top := maxOf(chart.f)
		for _, r := range rows {
			fmt.Printf("  %-32s %s %.2f%s\n", r.id, bar(chart.f(r), top, 40), chart.f(r), chart.unit)
		}
	}
}

func once(c llm.Client, model string) run {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	var r run
	for ev := range c.Stream(ctx, llm.Request{Model: model, Messages: []llm.Message{llm.UserText(prompt)}, MaxTokens: 8000}) {
		switch ev.Type {
		case llm.EventTextDelta, llm.EventThinkingDelta:
			if r.ttft == 0 {
				r.ttft = time.Since(start)
			}
			if ev.Type == llm.EventThinkingDelta {
				r.reasoningStreamed = true
			} else if r.firstText == 0 {
				r.firstText = time.Since(start)
			}
		case llm.EventDone:
			r.total = time.Since(start)
			switch ev.Message.Stop {
			case llm.StopLength:
				r.err = "hit max_tokens"
			case llm.StopError:
				r.err = ev.Message.Err
				if len(r.err) > 60 {
					r.err = r.err[:60] + "…"
				}
			}
			if u := ev.Message.Usage; u != nil {
				r.out, r.reasoning, r.cost = u.Output, u.Reasoning, u.CostUSD
			}
		}
	}
	return r
}

func bar(v, top float64, width int) string {
	if top <= 0 {
		return ""
	}
	eighths := int(v / top * float64(width*8))
	full, rem := eighths/8, eighths%8
	s := strings.Repeat("█", full)
	if rem > 0 {
		s += string([]rune(" ▏▎▍▌▋▊▉")[rem])
	}
	return s + strings.Repeat(" ", width-len([]rune(s)))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}
