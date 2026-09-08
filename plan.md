# Plan: Implement the Grovlet executable lifecycle

## Goal

Complete Task 002 by making `grovlet` a long-running process with an explicit
runtime directory, newline-delimited JSON readiness, clear startup failures,
and graceful SIGTERM shutdown. Verify the lifecycle through unit tests and a
real-process integration test without introducing the reusable Task 003 test
harness.

## Context

The repository is on branch `adiludmer/implement-mvp` with `origin/main` as
its base. Task 001 is complete in commits `e0b47e3` and `772314b`; the current
`cmd/grovlet` command only prints its name and exits. Task 002 in
`tasks/002-grovlet-executable-lifecycle.md` is the sole implementation scope.
The accepted Grovlet process model makes this process the node supervisor, but
worker supervision, node identity, membership, services, and transport remain
future tasks.

An unrelated `.idea/` directory is untracked and belongs to the user. Do not
modify or commit it.

### Key Files

- `tasks/002-grovlet-executable-lifecycle.md` — lifecycle scope and completion
  state.
- `cmd/grovlet/main.go` — flag parsing, runtime-directory preparation,
  lifecycle events, and signal-controlled entry point.
- `cmd/grovlet/main_test.go` — unit and direct-`os/exec` process coverage.
- `tasks/003-process-test-harness.md` — boundary for reusable process-testing
  support that must not be implemented yet.
- `tasks/011-node-identity-and-endpoint.md` — boundary for node identity and
  advertised readiness fields that must not be introduced here.

### Decisions Made

- Require `--runtime-dir` so every process receives explicit isolated state;
  create a missing directory with owner-only permissions and reject unusable
  paths before reporting readiness.
- Emit one JSON object per stdout line: `{"event":"ready"}` after startup
  validation and `{"event":"stopped"}` after graceful shutdown. Keep startup
  diagnostics human-readable on stderr.
- Handle SIGTERM through a cancellable context. Unit tests cancel the context
  directly; the integration test signals a real child process.
- Build the real binary once inside this package's tests using direct
  `os/exec`. Do not create reusable `grovetest` APIs before Task 003.
- Use bounded condition waits and channel synchronization only; no fixed sleeps.

## Sub-Tasks

- [x] 1. Select and bound Task 002.
  **Context:** Read `AGENTS.md`, `IMPLEMENTATION_PLAN.md`, Task 002, the process
  model, relevant SDK/demo guidance, and the Task 003 and Task 011 boundaries.
  **Outcome:** Limited the change to executable lifecycle behavior and chose a
  minimal JSON event protocol without worker, node, service, or cluster state.

- [ ] 2. Validate and prepare the runtime directory.
  **Context:** Parse `--runtime-dir` with an isolated `flag.FlagSet`, require a
  non-empty value, create missing directories, and return typed startup errors
  for invalid paths.
  **Acceptance:** Unit tests prove required-flag validation, directory creation,
  and typed failure when the selected path cannot be used as a directory.

- [ ] 3. Run until graceful shutdown.
  **Context:** Emit the ready event only after startup succeeds, block on the
  provided context, emit the stopped event after cancellation, and have `main`
  translate SIGTERM into that cancellation.
  **Acceptance:** A unit test observes readiness, proves the command remains
  active, cancels it, and observes clean shutdown without fixed sleeps.

- [ ] 4. Prove the real process lifecycle.
  **Context:** Build the actual `grovlet` binary once for this test package,
  launch it with a temporary runtime directory, decode readiness, send SIGTERM,
  and require the stopped event plus exit status zero. Separately run it with an
  invalid runtime path and require a non-zero exit with useful diagnostics.
  **Acceptance:** Integration tests use direct `os/exec`, bounded waits, isolated
  temporary paths, child cleanup, and captured diagnostics on failure.

- [ ] 5. Verify and close Task 002.
  **Context:** Review package documentation and tests, format the repository,
  run focused tests and all earlier tests, and mark only Task 002 DONE after all
  checks pass.
  **Acceptance:** The binary builds; `gofmt -l`, `go vet ./...`,
  `go test -race ./...`, and `go test ./...` pass; the working tree contains no
  unintended changes.

## Log

- 2026-09-08: Task 001 completed without deviations or architectural issues;
  `go vet ./...`, `go test ./...`, and race-enabled tests passed.
- 2026-09-08: Selected Task 002 and recorded the JSON lifecycle protocol,
  runtime-directory behavior, signal path, and adjacent-task boundaries.
