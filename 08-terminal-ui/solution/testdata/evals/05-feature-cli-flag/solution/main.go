package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("greet", flag.ContinueOnError)
	name := fs.String("name", "world", "who to greet")
	upper := fs.Bool("upper", false, "print in upper case")
	if err := fs.Parse(args); err != nil {
		return err
	}
	msg := fmt.Sprintf("hello, %s\n", *name)
	if *upper {
		msg = strings.ToUpper(msg)
	}
	fmt.Fprint(out, msg)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		os.Exit(2)
	}
}
