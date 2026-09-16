# Plan: Move Grove Shop debugging into its application console

## Goal

Complete Task 032 by aligning the existing two-worker Delve/DAP implementation
with Grove's application-console contract. The Grove Shop binary must start the
five-node debug topology, resolve and tunnel two independent debugger sessions
through TUI and structured actions, execute the documented breakpoint flow, and
finish with every worker healthy without requiring the separate `grove` CLI.

## Context

Task 031 is complete and provides the application-owned TUI/action registry,
foreground cluster controller, and shared status model. An earlier Task 032
implementation already provides System NATS debug streaming, node-local Delve
ownership, exact-worker supervision, DAP attach adaptation, and a real
five-Grovlet/two-breakpoint E2E, but its deployment and attach entry points live
under `cmd/grove`. This task preserves those proven mechanisms and moves their
operational ownership to `cmd/grovlet`, built and run as `groveshop`.

### Key Files

- `tasks/031-final-mvp-lifecycle-e2e.md` — current acceptance contract.
- `docs/cli/README.md` and `demo/DEMO_FLOW.md` — application-console and
  operator experience contracts.
- A focused Grove console package — typed action registration/invocation and
  deterministic TUI rendering shared by built-in and application actions.
- `cmd/grovlet/main.go` and focused console files — make the deployed Grove
  Shop/Grovlet executable select console, structured-action, or node-runtime
  mode without requiring another executable.
- `cmd/grove/mvp_test.go` — existing complete lifecycle proof to migrate from
  direct control-plane composition to Grove Shop binary actions.
- `demo/groveshop/web.go` — existing Web and structured status contract shared
  by the browser and console.
- `grovetest/` — real process ownership, readiness waits, isolation, cleanup,
  and diagnostics.
- `cmd/grove/debug.go` and `cmd/grove/debug_test.go` — existing service
  resolution, local DAP gateway, and complete debugger E2E to reuse and migrate.
- `cmd/grovlet/debug.go` — existing node-local Delve lifecycle bound to the
  exact worker generation.
- `internal/systemnats/debug.go` — existing full-duplex DAP transport over
  ephemeral System NATS subjects.
- `demo/DEBUGGING_DEMO.md` — exact application-binary commands and TUI paths
  that must be executed before Task 032 is complete.

### Decisions Made

- Keep `cmd/grovlet` as the application-runtime entry point and build it as
  `groveshop`; node flags retain headless Grovlet behavior while no arguments
  open the application console and `action ...` invokes structured operations.
- Use one typed action registry for both TUI selections and non-interactive
  actions. Grove Shop registers at least one application-specific action so the
  extension contract is exercised, not merely sketched.
- Let one foreground console process own local Grovlet children and lifecycle
  state. Short-lived action invocations reach that owner through a local Unix
  control endpoint discovered from an explicit state file; only connection
  metadata is local, while authoritative cluster state remains in System NATS
  JetStream/KV.
- Reuse the existing artifact, desired-state, placement, rollout, recovery,
  restart, rollback, and resilience paths. The application operation layer
  sequences them and reports structured results; it does not duplicate them.
- Keep the Web UI read-only. Browser polling, TUI rendering, and
  `cluster.status` consume the same `groveshop.ClusterStatusView`.
- Do not expose or adapt DAP/debug actions in Task 031. The preserved debugger
  machinery will be connected to this registry only when Task 032 resumes.
- Keep one ordinary Delve session per selected worker. Grove resolves topology,
  injects the node-local PID into DAP attach, and tunnels bytes; it does not
  multiplex DAP state or implement debugger semantics.
- Treat debugger attach as a long-running registered action: both TUI and Unix
  action clients receive the resolved endpoint before waiting, and cancellation
  closes the local listener, System NATS stream, Delve process, and debug state.
- Use the existing topology-explicit five-node demo placement only for Task 032;
  do not add affinity, anti-affinity, or a general scheduler.

## Sub-Tasks

- [x] 1. Define the shared application-console registry and TUI model.
  **Context:** Introduce the smallest Go package that registers typed Grove and
  application actions, dispatches them with structured arguments/results, and
  renders application-first navigation plus authoritative status. Unit tests
  must prove duplicate/unknown action errors and that TUI selection invokes the
  exact same registered handler as automation.
  **Outcome:** Added the dependency-free `console` package with a zero-value
  action registry, typed errors, deterministic action ordering, a console model,
  and TUI rendering/selection over the same handler. External-package tests and
  a runnable example pass with `go test -count=1 ./console` and `go vet
  ./console`.

- [x] 2. Make the Grove Shop artifact own console and node modes.
  **Context:** Extend the current Grovlet/Grove Shop entry point so the built
  artifact runs headlessly with node flags, opens the TUI without arguments,
  and dispatches `action ...`. Add foreground ownership, local action transport,
  connection discovery, signal cleanup, and one registered Grove Shop action.
  **Outcome:** The `cmd/grovlet` artifact now opens the Grove Shop console with
  no arguments, invokes `action ...` through a local Unix action server, and
  retains its existing headless node/worker modes. Grove Shop registers an
  application-owned order integrity action. Real subprocess tests exercised
  console, structured-action, and node roles from the same built binary and
  verified discovery cleanup; `go test -count=1 ./cmd/grovlet` passed.

- [x] 3. Drive good and broken rollouts through application actions.
  **Context:** Implement `rollout.start` and `cluster.status` over the existing
  artifact/config and System NATS lifecycle. The good configuration must start
  three real nodes, publish desired placement and active artifact/config state,
  and serve Web. The broken Inventory candidate must expose pending/failure/
  rollback through the shared read model while Artifact A stays known-good.
  **Outcome:** Registered `rollout.start` and `cluster.status` on the shared
  registry. The foreground controller embeds immutable artifacts, starts three
  real Grovlets, records desired/placement/deployment state in System NATS,
  serves Web, captures pending candidate state, and invokes the existing failed
  upgrade rollback path after the invalid Inventory process rejects its config.
  A real subprocess test completed orders before and after rollback and asserted
  structured artifact/config identities and failure reason.

- [x] 4. Expose recovery, durable restart, and resilience operations.
  **Context:** Add application-native operations used by the final E2E to kill
  a service-hosting node, wait for normal recovery, restart the cluster from
  the same durable directories, and run the existing Task 030 resilience flow.
  Keep bounded condition waits and return diagnostics from the operation.
  **Outcome:** Added `resilience.run` and `cluster.restart` to the application
  registry. The resilience action kills the active Inventory host, waits for
  existing recovery/placement convergence, persists recovered desired state,
  and proves another HTTP order. Restart reuses every Grovlet runtime directory,
  discovers the new System NATS URL, reconstructs durable intent, and returns
  the shared status healthy. The real-process action test passed through the
  full recovery/restart/rollback sequence.

- [x] 5. Prove and document the reopened Task 031 contract.
  **Context:** Migrate the complete MVP E2E so all lifecycle mutations enter
  through the Grove Shop binary. Assert TUI and Web render the same structured
  state, run the final documented application-console flow, update affected
  docs with implemented behavior, and mark only Task 031 DONE.
  **Acceptance:** Formatting, diff checks, focused repetitions, `go vet ./...`,
  `go test -count=1 ./...`, and `go test -race -count=1 ./...` all pass with
  useful real-process diagnostics and no fixed sleeps.
  **Outcome:** The application-native E2E now verifies the embedded Web asset,
  immutable runtime config identity, cross-Grovlet placement, the shared Web,
  action, and TUI read model, order completion, node recovery, durable cluster
  reconstruction, candidate failure, and rollback. Contextual TUI paths invoke
  the same registry as automation, and the docs show the runnable one-binary
  workflow. `go vet ./...`, `go test -count=1 ./...`, and `go test -race
  -count=1 ./...` pass.

- [x] 6. Extract the reusable service-aware DAP gateway.
  **Context:** Move the existing placement resolution, local listener, attach
  PID injection, and DAP proxy logic from `cmd/grove/debug.go` behind a focused
  internal package usable by both command entry points. Preserve the current
  generic CLI for compatibility, but do not extend its product surface.
  **Acceptance:** Gateway unit tests cover service resolution, DAP framing/PID
  injection, cancellation, and cleanup; existing `cmd/grove` debug tests pass.
  **Outcome:** Added `internal/debuggateway`, which owns authoritative service
  selection, local listener and remote stream lifetime, DAP framing, and attach
  PID injection. `cmd/grove` now delegates its compatibility command to this
  package, and the former gateway unit cases moved with the implementation.
  `go test -count=1 ./internal/debuggateway` and a compile-only
  `go test -run '^$' -count=1 ./cmd/grove` pass.

- [x] 7. Start the five-node debug topology from Grove Shop.
  **Context:** Register an application action for the debug demo and a matching
  `Deployments > Debug demo > Start` TUI path. Embed the selected config into
  the running debug-capable Grove Shop artifact, launch five real Grovlets with
  Web/Orders/Inventory/Payment/Shipping on nodes 1-5, and return the shared
  structured status only after every worker is healthy.
  **Acceptance:** A real application-binary test starts the topology, observes
  five healthy placements through `cluster.status`, and verifies the exact
  Orders/node-2 and Payment/node-4 worker identities.
  **Outcome:** Registered `debug.demo.start` and mapped `Deployments > Debug
  demo > Start` to it. The application controller embeds the Acme config into
  its own debug-built artifact, starts five real Grovlets with fixed demo
  placement, records the active artifact/rollout, and waits for the shared
  status model to report every node and worker healthy. The status contract now
  exposes worker identity. The real application-binary topology test and Grove
  Shop Web tests pass.

- [x] 8. Expose long-running debugger attachment through console actions.
  **Context:** Register `debug.attach SERVICE --listen ADDRESS` on the same
  registry used by TUI selection. Extend the local action protocol only enough
  to publish the initial resolved result before holding the action open, detect
  action-client cancellation, and propagate terminal session errors. Map the
  documented service-context TUI paths to the same handler.
  **Acceptance:** Unit/integration tests prove initial result streaming,
  cancellation cleanup, contextual path mapping, actionable resolution errors,
  and identical registry dispatch for TUI and automation.
  **Outcome:** Registered `debug.attach` on the application registry and mapped
  the documented service-instance TUI path to that same handler. The console
  action protocol now marks long-running sessions, writes the resolved JSON
  result before waiting, retains the invoking process until DAP disconnect,
  and cancels the gateway when that process closes. Parser, TUI mapping, and
  streaming-lifetime tests pass with `go vet ./cmd/grovlet`.

- [ ] 9. Prove and document the application-native two-worker debug flow.
  **Context:** Migrate the existing real Delve E2E so the Grove Shop console
  owns the cluster and both `debug.attach` subprocesses use the Grove Shop
  binary. Attach ordinary DAP clients, hit and inspect Orders and Payment on
  different workers during one order, continue, disconnect, verify non-target
  workers and final health, then execute every final command/TUI path in
  `demo/DEBUGGING_DEMO.md` exactly as documented.
  **Acceptance:** Focused repetitions, `go vet ./...`, `go test -count=1
  ./...`, and `go test -race -count=1 ./...` pass. Only then mark Task 032 DONE.

## Log

- 2026-09-14: `origin/main` advanced to `a02664f` and reopened Task 031 after
  adopting an application-console/TUI product contract. The old Task 032
  completion marker was reset; its implementation remains preserved for later
  adaptation.
- 2026-09-14: Rebased cleanly onto the new console-oriented `origin/main`, with
  upstream documentation taking precedence where the former separate-CLI
  debugging walkthrough conflicted.
- 2026-09-14: The shared `console` package now defines one public action and TUI
  contract without importing Grove runtime or control-plane implementation.
- 2026-09-14: The Grove Shop artifact now owns the foreground TUI/action server
  and the unchanged headless Grovlet runtime modes. Local state contains only
  the Unix control endpoint and is removed when the console exits.
- 2026-09-14: `rollout.start --config` now drives both the known-good Acme
  deployment and the deliberately broken candidate from the application
  binary. `cluster.status`, Web polling, and TUI model conversion share the
  existing `groveshop.ClusterStatusView`; the rollback operation records the
  Inventory validation field and retains Artifact A.
- 2026-09-14: `resilience.run` and `cluster.restart` now exercise recovery and
  durable reconstruction through the same console registry. Restart now selects
  the latest ready-event NATS URL from each reused node log, avoiding stale
  connection metadata after an embedded server restarts.
- 2026-09-16: Closed the reopened Task 031 contract. The final E2E proves the
  application artifact's embedded UI/config, cross-process business call,
  contextual TUI state, recovery, reconstruction, rejected candidate, and
  post-rollback order. Recovery waits for a successful application call after
  control-plane convergence, eliminating a responder-readiness race. The full
  normal and race suites pass.
- 2026-09-16: Started Task 032 alignment on `origin/main` `0b193bf`. The
  existing Delve controller, System NATS byte tunnel, supervision state, and DAP
  E2E remain the implementation baseline; only their separate-CLI ownership is
  being replaced by the application console contract.
- 2026-09-16: Extracted the service-aware local DAP gateway from `cmd/grove`
  into `internal/debuggateway` so Grove Shop can own sessions without copying
  service-resolution or DAP protocol adaptation logic.
- 2026-09-16: Grove Shop now owns the debug demo deployment. Its action starts
  Web, Orders, Inventory, Payment, and Shipping on nodes 1-5 from the configured
  application artifact and exposes worker IDs in the same status read model
  consumed by TUI and automation.
- 2026-09-16: `debug.attach` now runs as a long-lived application action. Both
  contextual TUI selection and structured action clients receive resolved
  service/node/worker/artifact/endpoint metadata before the ordinary DAP stream
  begins, and client cancellation unwinds the same session resources.
