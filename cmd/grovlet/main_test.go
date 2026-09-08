package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRun(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(&stdout); err != nil {
		t.Fatal(err)
	}

	const wantOutput = "grovlet\n"
	if got := stdout.String(); got != wantOutput {
		t.Errorf("output = %q; want %q", got, wantOutput)
	}

	wantErr := errors.New("write failed")
	if err := run(errorWriter{err: wantErr}); !errors.Is(err, wantErr) {
		t.Errorf("error = %v; want %v", err, wantErr)
	}
}

// This example captures the command's output without starting a process.
func Example() {
	var stdout bytes.Buffer
	if err := run(&stdout); err != nil {
		panic(err)
	}

	fmt.Print(stdout.String())
	// Output:
	// grovlet
}
