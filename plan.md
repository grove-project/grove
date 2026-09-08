# Plan: Implement the Grovlet process test harness

## Goal

Complete Task 003 by adding a reusable `grovetest` Go package that builds and
controls the real Grovlet executable. The harness must provide deterministic
readiness, graceful stop, forced kill, restart, logs, isolated temporary state,
cleanup, and startup-failure diagnostics without adding multi-node behavior.

## Context

The repository is on branch `adiludmer/implement-mvp` with `origin/main` as
its base. Tasks 001 and 002 are complete; Task 002 established the executable
contract consumed here: `grovlet --runtime-dir PATH` emits newline-delimited
`{"event":"ready"}` and `{"event":"stopped"}` records and handles SIGTERM.
Task 003 in `tasks/003-process-test-harness.md` is the sole implementation
scope. Task 004 owns cluster composition, node counts, multi-node diagnostics,
and unique node-level networking details.

### Key Files

- `tasks/003-process-test-harness.md` — required harness operations and tests.
- `grovetest/grovetest.go` — exported binary-build and node-process harness.
- `grovetest/grovetest_test.go` — runnable example and real-process harness
  self-test.
- `cmd/grovlet/main_test.go` — existing direct-`os/exec` integration coverage
  to migrate onto `grovetest` once the harness exists.
- `tasks/004-local-multi-grovlet-cluster-harness.md` — boundary for future
  multi-node orchestration.

### Decisions Made

- Keep `grovetest` as a public root package because later Grove application and
  E2E tests are explicit consumers of the harness.
- `BuildGrovlet` takes a context plus caller-owned output directory and derives
  the matching Grove module source directory, allowing one build to be reused
  throughout a test-package invocation.
- `StartNode` creates and owns one isolated runtime directory. `Restart` reuses
  that directory, while `Cleanup` forcibly stops any live process and removes
  the directory idempotently.
- `WaitReady`, `Stop`, and `Kill` take caller-controlled contexts. Timeouts and
  premature exits return a typed `ProcessError` containing captured logs.
- Preserve logs across restarts and synchronize log access so failure
  diagnostics are safe while a child is running.
- Keep node lifecycle sequential. Concurrent method calls and multi-node
  semantics are not promised by Task 003.
- Build the binary once in the harness self-test's `TestMain`; use no fixed
  sleeps or shell orchestration.

## Sub-Tasks

- [x] 1. Select and bound Task 003.
  **Context:** Read `AGENTS.md`, `IMPLEMENTATION_PLAN.md`, Task 003, the current
  Grovlet protocol, relevant SDK/demo testing guidance, and the Task 004
  boundary.
  **Outcome:** Chose a public, error-returning single-node process harness with
  context-bounded waits and no cluster, membership, service, or transport APIs.

- [x] 2. Build the matching Grovlet executable.
  **Context:** Add `BuildGrovlet(ctx, outputDir)` to compile `cmd/grovlet` from
  the module source associated with the `grovetest` package. Capture compiler
  output in a typed diagnostic error and leave output-directory lifecycle with
  the caller.
  **Outcome:** Added `BuildGrovlet`, which derives the matching module source,
  builds into a caller-owned directory, and returns `ProcessError` diagnostics.
  `grovetest` uses `TestMain` to build one binary shared by its entire suite.

- [x] 3. Control one real Grovlet process.
  **Context:** Add `StartNode`, `Node.WaitReady`, `Node.Stop`, `Node.Kill`,
  `Node.Restart`, `Node.Logs`, `Node.TempDir`, and `Node.Cleanup`. Parse the Task
  002 JSON lifecycle stream, retain combined output, preserve the runtime path
  across restarts, and return diagnostic errors on timeouts or process failure.
  **Outcome:** Added the complete documented single-node API in
  `grovetest/grovetest.go`. Contexts bound waits, a synchronized buffer retains
  output across restarts, and idempotent cleanup kills live children and removes
  their runtime directories.

- [x] 4. Prove the complete harness workflow.
  **Context:** Through a real binary, test startup/readiness, graceful stop,
  forced kill, successful restart after both stop modes, log retention, runtime
  directory reuse, cleanup of a live child, and invalid-runtime startup failure.
  Manipulate the stopped node's runtime path into a file to trigger the real
  Task 002 startup error after restart.
  **Outcome:** `TestNode` covers every required transition using one real binary
  and no sleeps. It verifies `ProcessError` startup logs and cleanup of both
  failed and live nodes; `ExampleNode` documents the normal lifecycle.

- [x] 5. Migrate the existing command integration test.
  **Context:** Replace the Task 002 test's package-local binary building and
  direct `os/exec` lifecycle plumbing with `grovetest` calls while retaining its
  real-process readiness and graceful-stop coverage.
  **Outcome:** `TestGrovletProcess` now uses `BuildGrovlet`, `StartNode`,
  `WaitReady`, and `Stop`. The package-local process helpers and direct
  `os/exec` usage were removed while the real-process contract remains covered.

- [x] 6. Verify and close Task 003.
  **Context:** Review exported docs and tests, format the repository, run focused
  harness tests plus all earlier tests, and mark only Task 003 DONE after every
  check succeeds.
  **Outcome:** Reviewed the complete `go doc` contract and test sources;
  `gofmt -l` reports no files; `go vet ./...`, ten consecutive `TestNode` runs,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass. Task 003 is
  marked DONE with no leaked processes or unintended changes.

## Log

- 2026-09-08: Tasks 001 and 002 completed without deviations or architectural
  issues; the Grovlet lifecycle and real-process tests pass.
- 2026-09-08: Selected Task 003 and recorded the public single-node API,
  diagnostics model, test-build reuse, Task 002 test migration, and Task 004
  boundary.
- 2026-09-08: The focused `grovetest` and migrated `cmd/grovlet` suites pass;
  every Task 003 process transition and cleanup path is covered without sleeps.
- 2026-09-08: Completed Task 003 after API documentation review, vet, race,
  repeated harness, and full-suite verification; no deviations or architectural
  issues found.
