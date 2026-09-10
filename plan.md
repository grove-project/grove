# Plan: Reconstruct deployments after cluster restart

## Goal

Complete Task 020 by restarting all three real Grovlets with their existing
runtime directories, recovering authoritative membership, placement, and
desired deployment state from embedded JetStream, and reconstructing Grove
Shop workers through desired-state reconciliation.

## Context

Tasks 001 through 019 are complete. Each Grovlet already stores embedded NATS
state under `<runtime-dir>/system-nats`, and `grovetest.Node.Restart` preserves
its runtime directory and launch arguments. Current startup writes boot
placement and starts boot components before reading desired state; Task 020
must distinguish first boot from restored intent. Task 021 owns the CLI and is
not part of this increment.

### Key Files

- `tasks/020-durable-control-state-and-cluster-restart.md` — restart recovery
  acceptance contract.
- `cmd/grovlet/main.go` — control-plane initialization and component startup
  ordering.
- `cmd/grovlet/main_test.go` — three-process stop/restart/reconstruction E2E.
- `grovetest/grovetest.go` — persistent per-node runtime directory and restart
  behavior reused by the E2E.
- `internal/systemnats/desired.go` — watcher-derived persisted intent used as
  the first-boot/restored-state discriminator.

### Decisions Made

- Treat a ready non-empty desired view as restored deployment intent only when
  reconciliation is enabled. First boot keeps the explicit placement flags.
- Initialize desired state before placement and workers. Restored nodes observe
  persisted desired state, open placement in observation-only mode, and start
  no boot workers; the existing reconciler starts assigned components.
- Keep membership self-registration on every process start. JetStream remains
  the only durable store; no Grove snapshot, manifest, or side database is
  introduced.
- Restart all three Grovlets with the same node runtime directories and fixed
  route ports, then reconnect test transports to their new ephemeral client
  listener URLs.

## Sub-Tasks

- [ ] 1. Gate startup on restored desired state.
  **Context:** Start the desired watcher before placement, condition-wait for
  its initial snapshot, and select first-boot or restoration behavior without
  fixed sleeps.
  **Acceptance:** Existing first-boot tests remain green and focused startup
  tests prove empty versus non-empty desired views select the right inputs.

- [ ] 2. Reconstruct the deployment from persisted control state.
  **Context:** On restored intent, observe persisted placement without writing
  boot assignments and let desired reconciliation start only assigned workers.
  **Acceptance:** Unit/integration tests preserve separation between persisted
  control records and fresh observed component generations.

- [ ] 3. Add the complete cluster restart E2E.
  **Context:** Deploy Grove Shop, write and observe desired state, verify an
  order, gracefully stop all nodes, restart the same Node objects, reconnect to
  new System NATS URLs, wait for desired/placement/component reconstruction,
  and verify another order.
  **Acceptance:** The scenario uses real processes, reused runtime directories,
  bounded condition waits, and diagnostics on every failure.

- [ ] 4. Verify and close Task 020.
  **Context:** Repeat the restart scenario and run formatting, diff checks,
  vet, the complete race suite, and uncached `go test ./...` before marking the
  task DONE.
  **Acceptance:** All checks pass without weakening historical tests.

## Log

- 2026-09-10: Tasks 001 through 019 are complete on `origin/main`.
- 2026-09-10: Selected desired-view-first startup so persisted KV, rather than
  boot flags or a new side store, drives reconstruction.
