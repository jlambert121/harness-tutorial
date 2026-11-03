// Command trace-view renders a JSONL event trace as an ASCII timeline on
// stdout and, with -html, as a self-contained HTML page.
package main

import (
	"flag"
	"fmt"
	"os"

	"harness/internal/trace"
)

func main() {
	htmlOut := flag.String("html", "", "also write an HTML timeline to this file")
	width := flag.Int("width", 60, "ASCII timeline width")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: trace-view [-html out.html] trace.jsonl")
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
	trace.RenderASCII(events, os.Stdout, *width)
	if *htmlOut != "" {
		out, err := os.Create(*htmlOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer out.Close()
		if err := trace.RenderHTML(events, out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", *htmlOut)
	}
}
