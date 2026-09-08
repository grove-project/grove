// Command grovlet is the executable entry point for a Grove node.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "grovlet: %v\n", err)
		os.Exit(1)
	}
}

func run(stdout io.Writer) error {
	_, err := fmt.Fprintln(stdout, "grovlet")
	return err
}
