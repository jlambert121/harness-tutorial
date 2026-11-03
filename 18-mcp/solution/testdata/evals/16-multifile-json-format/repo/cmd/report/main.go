package main

import (
	"io"
	"os"

	"task/internal/report"
)

func run(args []string, out io.Writer) error {
	rows := report.Collect([]string{"go", "rust", "go", "zig", "go", "rust"})
	return report.Text{}.Format(out, rows)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		os.Exit(1)
	}
}
