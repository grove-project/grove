# Plan: Debug two Grove Shop workers through ordinary DAP

## Goal

Complete Task 032 with a five-Grovlet Grove Shop deployment where every
service runs in its own worker process and two independent `grove debug`
commands resolve Orders and Payment by service name, expose local DAP
listeners, tunnel to node-local Delve sessions, hit and inspect both workers
during one order, disconnect cleanly, and leave the cluster healthy.

## Context

Task 031 completed the lifecycle baseline. The existing runtime already owns
authoritative service placement in JetStream/KV and worker subprocesses in the
selected Grovlet. Task 032 connects those two facts through ephemeral System
NATS messaging. Delve remains the DAP implementation; Grove supplies target
selection, attach identity, transport, lifecycle state, and diagnostics.

### Key Files

- `tasks/032-delve-multi-worker-debugging.md` and
  `docs/adr/009-dap-debugging-interface.md` — accepted behavior and scope.
- `internal/systemnats/debug.go` — ephemeral debug-session control and ordered
  byte transport between the CLI and selected Grovlet.
- `cmd/grovlet/component.go`, `cmd/grovlet/worker.go`, and a focused debug
  controller — worker identity, explicit debugging state, and node-local Delve
  lifecycle.
- `demo/groveshop/groveshop.go` and `cmd/grovlet/main.go` — make Inventory,
  Payment, Shipping, Orders, and Web five genuine distributed workers.
- `cmd/grove/debug.go` — service resolution, local DAP listener, attach target
  injection, stream forwarding, cleanup, and actionable output.
- `cmd/grove/deploy.go` — the deliberately narrow, foreground five-node local
  debug-demo launcher and discoverable local connection metadata.
- `cmd/grove/debug_test.go` — deterministic real-process two-worker DAP proof.
- `demo/DEBUGGING_DEMO.md` and `configs/acme.yaml` — executable human flow.

### Decisions Made

- Keep placement authoritative in existing JetStream/KV. Debug sessions are
  ephemeral and use System NATS core messaging; no debugger state is written
  as durable cluster truth.
- Run one ordinary `dlv dap` process per selected worker. Each Delve listener
  is a node-local Unix socket and only its byte stream crosses System NATS, so
  no remote Delve port is exposed.
- Rewrite only the selected DAP `attach` request's process ID at the local Grove
  gateway. This hides PID discovery while leaving initialize, breakpoints,
  stack/variable requests, continue, events, and disconnect as ordinary Delve
  DAP traffic. Grove does not multiplex or reinterpret debugger state.
- Give every worker an observable generation-derived identity and record its
  artifact/version. Add an explicit `debugging` component state; normal exit
  supervision remains active, while intentional debugger pauses remain
  healthy and do not trigger recovery.
- Preserve the existing `NewGroveOrders` contract and add a separate fully
  distributed constructor for the debug topology. Payment and Shipping become
  explicitly registered workers without changing ordinary business types.
- Implement `grove deploy --config ... --debug-demo` as a foreground local-demo
  owner. It builds a configured copy of `./bin/grove-shop`, starts five real
  Grovlets through production command paths, writes local discovery metadata,
  prints the Web endpoint, and cleans up all children on interruption. This is
  deterministic demo placement, not a general deployment daemon or scheduler.
- Let status/components/debug commands use explicit connection flags as before
  or the local debug-demo metadata when flags are omitted. The metadata is only
  CLI discovery data; cluster truth continues to come from System NATS.
- Build Delve once inside the E2E from the pinned Go module dependency and pass
  its path to Grovlets. Human runs use `dlv` from `PATH` and fail immediately
  with an actionable diagnostic when it is unavailable.

## Sub-Tasks

- [ ] 1. Make the five-worker topology real.
  **Context:** Register Payment and Shipping workers, route all Orders
  dependencies through the existing Grove call path in the dedicated topology,
  expose worker/artifact identity, and cover the behavior with focused tests.
  **Acceptance:** Five explicit placements start five distinct healthy worker
  PIDs on node-1 through node-5, and an order crosses all four service calls.

- [ ] 2. Carry one selected Delve DAP stream through Grove.
  **Context:** Add the ephemeral System NATS session protocol, node-local Delve
  startup/cleanup, component `debugging` state, local CLI listener, attach PID
  injection, worker-exit handling, and diagnostics for missing/ambiguous/down
  targets or Delve failures.
  **Acceptance:** Focused protocol, lifecycle, and CLI tests prove independent
  streams, deterministic state transitions, cleanup, and error contracts.

- [ ] 3. Prove two concurrent workers with real DAP.
  **Context:** Use `grovetest` to launch five real Grovlet processes and a small
  Go DAP test client to initialize, attach, set breakpoints, inspect variables,
  continue, and disconnect from Orders and Payment during one HTTP order.
  **Acceptance:** Both endpoints hit only their intended worker on distinct
  nodes/PIDs, non-target workers remain healthy, the order completes after both
  continues, sessions disappear, and all five components return healthy.

- [ ] 4. Ship and execute the human debug demo.
  **Context:** Add the narrow foreground debug-demo deploy command, canonical
  Acme config, local command discovery, and update the guide to exact output and
  cleanup behavior.
  **Acceptance:** Every documented shell command is run from a clean checkout;
  two ordinary DAP clients hit Orders and Payment and final status is healthy.

- [ ] 5. Verify and close Task 032.
  **Context:** Run formatting, diff checks, focused repetitions, vet, uncached
  full tests, and the race suite. Mark the task DONE only after the automated
  and human contracts both pass, then rebase and push directly to `main`.

## Log

- 2026-09-13: Rebased onto `origin/main` at `1bfb704`; upstream added the final
  Task 032 contract and clarified Task 031 as its lifecycle baseline.
- 2026-09-13: Confirmed Delve 1.27.2 is available locally and supports DAP over
  Unix sockets. Existing placement provides service-to-node resolution, while
  the component manager is the sole owner of the target worker process.
- 2026-09-13: Preserved Task 031's completed plan in git history. Task 032 will
  not introduce durable debug state, a general scheduler, SSH, remote Delve
  ports, a DAP multiplexer, or cross-service stepping.
