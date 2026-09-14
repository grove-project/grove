# Plan: Move the Grove Shop lifecycle into its application console

## Goal

Complete the reopened Task 031 by making the Grove Shop application binary its
own operational console. The same binary must provide a TUI and structured
actions over one registry, drive the existing deployment, recovery, durable
restart, rollback, and resilience behavior, expose the authoritative Grove read
model, and pass one production-shaped real-process lifecycle E2E.

## Context

`origin/main` reopened Task 031 and replaced the separate-CLI MVP experience
with an application-owned console. The already implemented Task 032 debugger
transport is preserved on this branch, but Task 032 is pending again and its
console adaptation is explicitly outside this task. Task 031 may reuse existing
runtime/control-plane mechanisms; it must not create another rollout engine,
control store, scheduler, or same-host test shortcut.

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

- [ ] 4. Expose recovery, durable restart, and resilience operations.
  **Context:** Add application-native operations used by the final E2E to kill
  a service-hosting node, wait for normal recovery, restart the cluster from
  the same durable directories, and run the existing Task 030 resilience flow.
  Keep bounded condition waits and return diagnostics from the operation.
  **Acceptance:** Each operation is invoked through the registry/action path,
  preserves cross-Grovlet orders, and ends with the shared status healthy.

- [ ] 5. Prove and document the reopened Task 031 contract.
  **Context:** Migrate the complete MVP E2E so all lifecycle mutations enter
  through the Grove Shop binary. Assert TUI and Web render the same structured
  state, run the final documented application-console flow, update affected
  docs with implemented behavior, and mark only Task 031 DONE.
  **Acceptance:** Formatting, diff checks, focused repetitions, `go vet ./...`,
  `go test -count=1 ./...`, and `go test -race -count=1 ./...` all pass with
  useful real-process diagnostics and no fixed sleeps.

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
