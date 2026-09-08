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

- [ ] 2. Add isolated node resources and metadata.
  **Context:** Refactor unstarted node construction for cluster use; expose the
  node's harness ID and reserved port while preserving standalone `StartNode`.
  **Outcome:** Pending.

- [ ] 3. Add cluster lifecycle and diagnostics.
  **Context:** Implement construction, indexed access, start, readiness, stop,
  diagnostics, rollback, and idempotent cleanup around the existing Node API.
  **Outcome:** Pending.

- [ ] 4. Prove three-process isolation and cleanup.
  **Context:** Launch three real Grovlets, prove unique resources and readiness,
  kill one, re-check the two survivors, stop them gracefully, and prove runtime
  directories and reserved ports are released during cleanup.
  **Outcome:** Pending.

- [ ] 5. Verify and close Task 004.
  **Context:** Review exported docs and tests, format, vet, run focused tests
  repeatedly and under the race detector, run `go test ./...`, then mark Task
  004 DONE only after every check succeeds.
  **Outcome:** Pending.

## Log

- 2026-09-08: Tasks 001 through 003 completed; the repository, Grovlet
  lifecycle, and single-process test harness pass their full suites.
- 2026-09-08: Selected Task 004 and recorded the harness/runtime identity
  boundary, real port reservation strategy, lifecycle rollback, and E2E proof.
