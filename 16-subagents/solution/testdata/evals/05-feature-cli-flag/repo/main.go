package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("greet", flag.ContinueOnError)
	name := fs.String("name", "world", "who to greet")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(out, "hello, %s\n", *name)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		os.Exit(2)
	}
}
