# Plan: Implement the local multi-Grovlet cluster harness

## Goal

Complete Task 004 by composing the existing real-process `grovetest.Node`
harness into a local cluster that starts N independent Grovlets, gives every
node isolated harness identity, loopback port, and runtime state, waits for
readiness without sleeps, preserves failure diagnostics, and cleans up every
resource automatically.

## Context

The repository is on branch `adiludmer/implement-mvp` with `origin/main` as
its base. Tasks 001 through 003 are complete. Task 003 established the public
single-process lifecycle API and the Grovlet's newline-delimited ready/stopped
protocol. Task 004 is the sole implementation scope until its complete suite
passes. Task 011 still owns runtime node identity and advertised endpoints, so
this task's IDs and ports remain test-harness metadata and are not introduced
as Grovlet configuration or membership.

### Key Files

- `tasks/004-local-multi-grovlet-cluster-harness.md` — required cluster API,
  isolation, diagnostics, and three-process E2E.
- `grovetest/grovetest.go` — existing public node harness and the minimal node
  metadata/construction changes needed for cluster composition.
- `grovetest/cluster.go` — cluster construction, lifecycle, resource
  reservation, and diagnostics.
- `grovetest/cluster_test.go` — real three-Grovlet isolation and failure E2E.
- `tasks/011-node-identity-and-endpoint.md` — boundary for future runtime-level
  identity and advertised endpoint behavior.

### Decisions Made

- Add `NewCluster(binaryPath, nodeCount)` plus `NodeCount`, `Start`, `Node`,
  `WaitAllReady`, `Stop`, `DumpDiagnostics`, and idempotent `Cleanup`.
- Allocate deterministic harness IDs (`node-1`, `node-2`, ...), one isolated
  runtime directory, and one real reserved loopback TCP port per node.
- Keep each port reservation open until cluster cleanup so concurrent test
  processes cannot claim it. The current lifecycle-only Grovlet does not bind
  or advertise the port; Task 011 will introduce that process contract.
- Return typed `ClusterError` values that identify the operation and node and
  include a complete cluster diagnostic dump. Do not assert error strings.
- Start nodes sequentially and roll back all resources if any start fails.
  Stop every still-running node even when a sibling was forcibly killed.
- Keep cluster lifecycle calls sequential, matching the existing `Node`
  contract. Captured logs remain safe to read while processes run.
- Build the Grovlet once through the existing package `TestMain`; use bounded
  contexts and lifecycle events rather than sleeps.

## Sub-Tasks

- [x] 1. Select and bound Task 004.
  **Context:** Read `AGENTS.md`, `IMPLEMENTATION_PLAN.md`, Task 004, the current
  `grovetest` public contract, relevant SDK/demo guidance, and the Task 011
  boundary.
  **Outcome:** Chose a harness-only cluster abstraction with reserved ports and
  stable test IDs, without membership or new Grovlet flags.

- [x] 2. Add isolated node resources and metadata.
  **Context:** Refactor unstarted node construction for cluster use; expose the
  node's harness ID and reserved port while preserving standalone `StartNode`.
  **Outcome:** Added shared unstarted-node construction plus documented `ID`
  and `Port` accessors. Standalone nodes retain empty/zero metadata, while a
  Cluster assigns deterministic IDs, real reserved ports, and distinct temp
  directories.

- [x] 3. Add cluster lifecycle and diagnostics.
  **Context:** Implement construction, indexed access, start, readiness, stop,
  diagnostics, rollback, and idempotent cleanup around the existing Node API.
  **Outcome:** Added the complete `Cluster` API and typed `ClusterError`
  diagnostics. Partial startup rolls back resources, graceful stop skips
  already-exited siblings, and idempotent cleanup kills children, removes
  state, and releases reservations.

- [x] 4. Prove three-process isolation and cleanup.
  **Context:** Launch three real Grovlets, prove unique resources and readiness,
  kill one, re-check the two survivors, stop them gracefully, and prove runtime
  directories and reserved ports are released during cleanup.
  **Outcome:** `TestNewCluster` launches three real Grovlets, checks every
  isolated resource, kills the middle process, proves both survivors remain
  ready and stop cleanly, inspects diagnostics, and proves cleanup removes all
  runtime paths and releases every port.

- [x] 5. Verify and close Task 004.
  **Context:** Review exported docs and tests, format, vet, run focused tests
  repeatedly and under the race detector, run `go test ./...`, then mark Task
  004 DONE only after every check succeeds.
  **Outcome:** Reviewed the complete `go doc` surface; `gofmt -l` reports no
  files; `go vet ./...`, 25 consecutive cluster E2Es, `go test -race -count=1
  ./...`, and `go test -count=1 ./...` pass. Task 004 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 003 completed; the repository, Grovlet
  lifecycle, and single-process test harness pass their full suites.
- 2026-09-08: Selected Task 004 and recorded the harness/runtime identity
  boundary, real port reservation strategy, lifecycle rollback, and E2E proof.
- 2026-09-08: Repeated cluster execution exposed and fixed a Task 003 readiness
  race after forced exit; readiness now consistently reports a killed process.
- 2026-09-08: Completed Task 004 after focused, repeated, race, vet, and full
  suite verification; no scope deviations or architectural issues found.
