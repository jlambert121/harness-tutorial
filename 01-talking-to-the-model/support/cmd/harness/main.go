// Command harness is a streaming chat REPL (no tools yet).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"harness/internal/llm"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	model := flag.String("model", env("HARNESS_MODEL", "anthropic/claude-sonnet-5.5"), "gateway model id")
	base := flag.String("base", env("HARNESS_BASE_URL", "https://ai-gateway.vercel.sh/v1"), "gateway base URL")
	system := flag.String("system", "You are a concise assistant.", "system prompt")
	reasoning := flag.String("reasoning", "", `"" | enabled | low | medium | high`)
	flag.Parse()

	client := llm.NewGateway(*base, os.Getenv("AI_GATEWAY_API_KEY"))
	var history []llm.Message
	var total llm.Usage
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	fmt.Printf("model %s — Ctrl-C cancels a reply, Ctrl-D exits\n", *model)
	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		history = append(history, llm.UserText(line))

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		var msg *llm.Message
		thinking := false
		for ev := range client.Stream(ctx, llm.Request{Model: *model, System: *system, Messages: history, Reasoning: *reasoning}) {
			switch ev.Type {
			case llm.EventThinkingDelta:
				if !thinking {
					fmt.Print("\x1b[2m")
					thinking = true
				}
				fmt.Print(ev.Text)
			case llm.EventTextDelta:
				if thinking {
					fmt.Print("\x1b[0m\n")
					thinking = false
				}
				fmt.Print(ev.Text)
			case llm.EventDone:
				msg = ev.Message
			}
		}
		stop()
		if thinking {
			fmt.Print("\x1b[0m")
		}
		fmt.Println()
		switch msg.Stop {
		case llm.StopError:
			fmt.Printf("[error: %s]\n", msg.Err)
			history = history[:len(history)-1]
			continue
		case llm.StopAborted:
			fmt.Println("[cancelled]")
		}
		history = append(history, *msg)
		if u := msg.Usage; u != nil {
			total.Add(*u)
			fmt.Printf("\x1b[2m[%s · in %d (cached %d) · out %d · $%.5f · session $%.5f]\x1b[0m\n",
				msg.Model, u.Input, u.CacheRead, u.Output, u.CostUSD, total.CostUSD)
		}
	}
}
