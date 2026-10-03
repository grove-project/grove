// Package testbin provides what real-process tests need built, such as the
// Grovlet and grove binaries, on first use instead of in TestMain. Getting one
// skips the calling test under -short, so `go test -short ./...` runs only
// in-process tests and builds nothing. It is imported only by tests.
package testbin

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const buildTimeout = 3 * time.Minute

var (
	dirOnce sync.Once
	dir     string
	dirErr  error
)

// Artifact is one built file or resolved tool path, produced at most once per
// test binary.
type Artifact struct {
	name    string
	produce func(tb testing.TB) (string, error)
	once    sync.Once
	value   string
	err     error
}

// New returns an artifact that build produces in its own temporary directory,
// so artifacts with the same file name never collide.
func New(name string, build func(ctx context.Context, dir string) (string, error)) *Artifact {
	return &Artifact{name: name, produce: func(testing.TB) (string, error) {
		dirOnce.Do(func() { dir, dirErr = os.MkdirTemp("", "grove-testbin-") })
		if dirErr != nil {
			return "", dirErr
		}
		own, err := os.MkdirTemp(dir, "artifact-")
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		return build(ctx, own)
	}}
}

// Derive returns an artifact computed from parent's value, such as a binary's
// artifact digest.
func Derive(parent *Artifact, name string, derive func(string) (string, error)) *Artifact {
	return &Artifact{name: name, produce: func(tb testing.TB) (string, error) {
		if tb == nil {
			return derive(parent.ForExample())
		}
		return derive(parent.Get(tb))
	}}
}

// Get returns the artifact, producing it on first use. Needing an artifact
// marks a real-process test, so Get skips tb under -short.
func (a *Artifact) Get(tb testing.TB) string {
	tb.Helper()
	RequireProcesses(tb)
	a.once.Do(func() { a.value, a.err = a.produce(tb) })
	if a.err != nil {
		tb.Fatalf("build %s: %v", a.name, a.err)
	}
	return a.value
}

// ForExample returns the artifact for an Example function, which has no
// testing.TB to skip or fail. TestMain must call SkipExamplesUnderShort for
// that example, and a failed build panics.
func (a *Artifact) ForExample() string {
	a.once.Do(func() { a.value, a.err = a.produce(nil) })
	if a.err != nil {
		panic(fmt.Sprintf("build %s: %v", a.name, a.err))
	}
	return a.value
}

// SkipExamplesUnderShort adds the named real-process examples to -skip when
// the test binary runs with -short. Call it from TestMain before m.Run.
func SkipExamplesUnderShort(names ...string) {
	if !flag.Parsed() {
		flag.Parse()
	}
	if !testing.Short() || len(names) == 0 {
		return
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	if current := flag.Lookup("test.skip").Value.String(); current != "" {
		pattern = current + "|" + pattern
	}
	if err := flag.Set("test.skip", pattern); err != nil {
		panic(err)
	}
}

// RequireProcesses skips tb under -short. Call it at the start of a test that
// starts real processes without needing a built artifact.
func RequireProcesses(tb testing.TB) {
	tb.Helper()
	if testing.Short() {
		tb.Skip("starts real Grove processes; skipped by -short")
	}
}

// Cleanup removes every built artifact. Call it from TestMain after m.Run.
func Cleanup() error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}
