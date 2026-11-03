// Command replay plays a recorded JSONL trace through the TUI at its original
// pace (or faster), with no model and no tools: the UI is a pure function of events.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"harness/internal/trace"
	"harness/internal/ui/tui"
)

func main() {
	speed := flag.Float64("speed", 1, "playback speed multiplier")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: replay [-speed 2] trace.jsonl")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	events, err := trace.ReadEvents(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m := tui.FromEvents(nil, 80, 24)
	p := tea.NewProgram(m)
	go func() {
		for i, e := range events {
			if i > 0 {
				gap := e.Time.Sub(events[i-1].Time)
				time.Sleep(time.Duration(float64(min(gap, 5*time.Second)) / *speed))
			}
			p.Send(tui.EventMsg{Event: e})
		}
	}()
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
