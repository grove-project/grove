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

An unrelated `.idea/` directory was present at task start and belongs to the
user. Do not modify or commit it.

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

- [x] 2. Validate and prepare the runtime directory.
  **Context:** Parse `--runtime-dir` with an isolated `flag.FlagSet`, require a
  non-empty value, create missing directories, and return typed startup errors
  for invalid paths.
  **Outcome:** `cmd/grovlet/main.go` now requires `--runtime-dir`, creates it
  with owner-only permissions, rejects positional arguments, and returns a
  `runtimeDirError` for unusable paths. `TestParseConfig` and
  `TestPrepareRuntimeDir` pass.

- [x] 3. Run until graceful shutdown.
  **Context:** Emit the ready event only after startup succeeds, block on the
  provided context, emit the stopped event after cancellation, and have `main`
  translate SIGTERM into that cancellation.
  **Outcome:** `run` emits newline-delimited JSON lifecycle events and blocks on
  its context; `main` derives that context from SIGTERM. `TestRun` uses
  `testing/synctest` to verify readiness, continued liveness, shutdown, and
  output-error propagation without sleeps.

- [x] 4. Prove the real process lifecycle.
  **Context:** Build the actual `grovlet` binary once for this test package,
  launch it with a temporary runtime directory, decode readiness, send SIGTERM,
  and require the stopped event plus exit status zero. Separately run it with an
  invalid runtime path and require a non-zero exit with useful diagnostics.
  **Outcome:** `TestGrovletProcess` builds one real binary, verifies the complete
  SIGTERM lifecycle, and verifies invalid-path startup failure. It uses bounded
  contexts, temporary paths, deferred child cleanup, and stderr diagnostics;
  the focused integration run passes.

- [x] 5. Verify and close Task 002.
  **Context:** Review package documentation and tests, format the repository,
  run focused tests and all earlier tests, and mark only Task 002 DONE after all
  checks pass.
  **Outcome:** Package docs and tests were reviewed; the binary builds;
  `gofmt -l` reports no files; `go vet ./...`, `go test -race -count=1 ./...`,
  ten consecutive real-process test runs, and `go test -count=1 ./...` pass.
  Task 002 is marked DONE and the unrelated `.idea/` content was not included.

## Log

- 2026-09-08: Task 001 completed without deviations or architectural issues;
  `go vet ./...`, `go test ./...`, and race-enabled tests passed.
- 2026-09-08: Selected Task 002 and recorded the JSON lifecycle protocol,
  runtime-directory behavior, signal path, and adjacent-task boundaries.
- 2026-09-08: Runtime-directory, lifecycle, and real-process tests pass; no
  fixed sleeps or reusable Task 003 harness APIs were introduced.
- 2026-09-08: Completed Task 002 after format, vet, race, repeated integration,
  and full-suite verification; no deviations or architectural issues found.
