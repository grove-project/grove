# Plan: Prove the CLI-driven local cluster lifecycle

## Goal

Complete Task 022 by exercising the real `grove` binary against three real
Grovlet processes: inspect cluster nodes and component placement, stop and
restart Inventory through the CLI, and verify the distributed Grove Shop order
flow recovers. Keep deployment and upgrade commands out of this increment.

## Context

Tasks 001 through 021 are complete on `origin/main`. Task 021 introduced the
`status`, `nodes`, `components`, and `component start|stop` commands and a
real-process status E2E. Existing Grovlet workers already run Orders and
Inventory as separate OS processes and route application calls through System
NATS using authoritative placement.

### Key Files

- `tasks/022-cli-driven-local-cluster-e2e.md` — current acceptance contract.
- `cmd/grove/main_test.go` — real CLI and three-Grovlet E2E to extend.
- `cmd/grove/main.go` — accepted CLI behavior exercised without redesign.
- `cmd/grovlet/worker.go` — real Grove Shop worker processes used by the test.
- `demo/groveshop/` — deterministic application contract used to verify
  recovery.

### Decisions Made

- Extend the existing real CLI E2E instead of starting a second identical
  three-node cluster. The expanded scenario permanently retains Task 021's
  status proof while adding Task 022's operator lifecycle.
- Treat `grove components` as the CLI placement inspection: its deterministic
  rows map each stable service ID to the Grovlet hosting it and include current
  lifecycle state.
- Verify that Orders cannot complete while Inventory is stopped, then create a
  completed order after the CLI restarts Inventory. Application verification
  uses the public `grove.Call` path over the same clustered System NATS
  transport, not a same-process shortcut.
- Use bounded condition waits and command contexts throughout. Preserve the
  existing concurrent process cleanup and include all Grovlet logs on failure.

## Sub-Tasks

- [x] 1. Generalize real CLI invocation helpers.
  **Context:** Extract bounded helpers that execute `grove`, wait for exact
  converged output, and report command output plus Grovlet logs on failure.
  **Outcome:** The E2E now shares bounded real-binary invocation and exact-output
  convergence helpers. Read commands may retry while state converges; lifecycle
  commands execute exactly once.

- [x] 2. Exercise the operator lifecycle.
  **Context:** Inspect healthy status, nodes, and component placement; stop
  Inventory through the real CLI; observe stopped placement and failed Orders
  behavior; restart Inventory; observe the incremented healthy generation.
  **Outcome:** The real CLI reports all three healthy nodes and exact Orders and
  Inventory placement, stops Inventory at generation 2, exposes the stopped
  state, and restarts it healthy at generation 3. An Orders request fails while
  Inventory is stopped.

- [x] 3. Verify the recovered reference application.
  **Context:** Connect through System NATS from the observer side and complete a
  deterministic Grove Shop order through Orders after Inventory restarts.
  **Outcome:** An observer-side placement client routes a public `grove.Call`
  through Orders to the restarted Inventory worker and returns a completed
  order with the expected reservation.

- [ ] 4. Verify and close Task 022.
  **Context:** Run formatting, diff checks, focused repeated E2E runs, vet, the
  full uncached suite, and the full race suite before marking the task DONE.

## Log

- 2026-09-11: Task 021 was rebased onto the latest `main`, passed vet plus full
  non-race and race suites, and was pushed to `main` at `cddfd77`.
- 2026-09-11: Confirmed Task 022 needs no new production command or control
  protocol; it closes the operator-lifecycle proof using the accepted CLI and
  runtime behavior.
- 2026-09-11: Expanded the existing three-Grovlet CLI E2E into the complete
  operator lifecycle. Three consecutive focused runs completed in
  10.69–15.41 seconds; the focused race run completed in 15.79 seconds.
