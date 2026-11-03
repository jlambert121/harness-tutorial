package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"task/internal/report"
)

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	format := fs.String("format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var f report.Formatter
	switch *format {
	case "text":
		f = report.Text{}
	case "json":
		f = report.JSON{}
	default:
		return fmt.Errorf("unknown format %q", *format)
	}
	rows := report.Collect([]string{"go", "rust", "go", "zig", "go", "rust"})
	return f.Format(out, rows)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		os.Exit(1)
	}
}
