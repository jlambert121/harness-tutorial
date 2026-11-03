package main

import (
	"fmt"
	"os"
)

func main() {
	cfg, err := LoadConfig(os.Getenv("APP_CONFIG"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("listening on %s:%d\n", cfg.Host, cfg.Port)
}
