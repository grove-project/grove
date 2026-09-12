package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGroveTestCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := runGroveCommand(ctx, "test", "--binary", grovletPath)
	if err != nil {
		t.Fatalf("grove test: %v; output=%q", err, output)
	}
	want := "Grove Shop E2E\n✓ cluster ready\n✓ order completed\nPASS\n"
	if output != want {
		t.Errorf("grove test output = %q; want %q", output, want)
	}
}

func TestGroveTestCommandReturnsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output, err := runGroveCommand(ctx, "test", "--binary", t.TempDir()+"/missing")
	if err == nil {
		t.Fatal("grove test accepted a missing artifact")
	}
	if !strings.Contains(output, "grove: inspect test artifact") {
		t.Errorf("failed grove test output = %q", output)
	}
}
