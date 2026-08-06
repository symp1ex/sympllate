package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/image/font/gofont/goregular"
)

func main() {
	output := flag.String("output", "", "path to regular.ttf")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "-output is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, goregular.TTF, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
