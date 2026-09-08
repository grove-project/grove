# Plan: Establish the Grove repository skeleton

## Goal

Complete Task 001 by creating the minimal Go module and `grovlet` command
needed for a clean checkout to pass `go test ./...`. Keep the implementation
strictly within the repository-wiring scope and leave process lifecycle,
configuration, networking, and services to later tasks.

## Context

The repository is on branch `adiludmer/implement-mvp` with `origin/main` as
its base. Every numbered task is currently TODO, so Task 001 in
`tasks/001-repository-skeleton.md` is the only task in scope. The accepted SDK
and demo contracts constrain future work but do not require SDK or Grove Shop
packages for this foundational task.

### Key Files

- `tasks/001-repository-skeleton.md` — scope, exclusions, and completion state.
- `go.mod` — root module declaration for `github.com/grove-project/grove`.
- `cmd/grovlet/main.go` — minimal executable entry point and testable command
  seam.
- `cmd/grovlet/main_test.go` — starter command contract and runnable example.

### Decisions Made

- Keep the command implementation in `cmd/grovlet`; no internal packages are
  justified until later tasks introduce reusable runtime behavior.
- Give the skeleton deterministic output and propagate writer failures so the
  starter test verifies behavior rather than only compilation.
- Do not add readiness, signal handling, runtime directories, flags, or
  long-running behavior; those belong to Task 002.

## Sub-Tasks

- [x] 1. Select and bound the first MVP task.
  **Context:** Read `AGENTS.md`, `IMPLEMENTATION_PLAN.md`, Task 001, the adjacent
  Task 002 boundary, and the relevant SDK/demo overview contracts.
  **Outcome:** Confirmed Task 001 is the first incomplete task and limited the
  implementation to module wiring, `cmd/grovlet`, and starter tests.

- [x] 2. Establish the Go module and minimal command.
  **Context:** Declare module `github.com/grove-project/grove` at the repository
  root. Implement a thin `cmd/grovlet` entry point with no lifecycle or runtime
  features.
  **Outcome:** Added `go.mod` and `cmd/grovlet/main.go`. A clean binary build
  succeeds, prints the deterministic command identity, and introduces no
  dependencies or runtime package structure.

- [x] 3. Define the starter command contract.
  **Context:** Test the command through its writer seam, including deterministic
  output and propagation of write failures. Include a runnable package example.
  **Outcome:** Added `cmd/grovlet/main_test.go` with `TestRun` covering output
  and writer-error propagation plus a runnable `Example`. The focused test run
  passes.

- [x] 4. Verify and close Task 001.
  **Context:** Format and inspect the Go package, run repository-wide tests, and
  change only Task 001's status after every acceptance check succeeds.
  **Outcome:** `gofmt -l` reports no files, `go vet ./...` and `go test ./...`
  pass, the command builds and runs from a temporary output directory, and
  `tasks/001-repository-skeleton.md` is marked DONE.

## Log

- 2026-09-08: Selected Task 001 as the first TODO item and recorded the
  repository-skeleton implementation boundary.
- 2026-09-08: Completed Task 001 without deviations or architectural issues;
  all focused and repository-wide verification passed.
