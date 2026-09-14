# Grove Shop MVP Implementation Guide

## Purpose
This file tells implementation agents how the demo contracts map onto the numbered Grove tasks.

The `demo/` folder defines the target behavior. The `tasks/` folder remains the source of truth for what may be implemented in each increment.

Do not implement the whole demo at once.

## Incremental mapping

### Foundation
- Task 005: create Grove Shop business-domain code only.

### Runtime
- Tasks 006-010: progressively wire Grove Shop service calls through Grove's explicit service registry, invocation envelope, System NATS transport, and cross-node dispatch.
- Keep the Orders business flow unchanged while replacing local plumbing with Grove runtime plumbing.

### Cluster awareness, placement, and recovery
- Tasks 011-014: establish node identity, membership, and health.
- Task 015: add explicit placement plus the SDK placement-validation contract. Each candidate Grovlet evaluates service eligibility locally. No validator means eligible everywhere; failed validation means that node cannot host the service.
- Task 016: enforce the placement invariant in component lifecycle. A Grovlet must never start or restart a service that is not currently eligible there.
- Tasks 017-020: detect failures, recover components, and preserve desired deployment state while reusing the same eligibility rules.
- Placement validation is a hard capability constraint. Scheduler/placement policy may choose only from the eligible node set.
- The demo UI should not be used as a source of truth. All state it eventually displays must come from Grove's structured control-plane read model.

### Placement validation test shape
MVP tests must cover the semantics even if the public Grove Shop demo does not yet depend on a real customer LAN:
- a service with no validator is eligible on every Grovlet;
- a deterministic validator can pass on one Grovlet and fail on another;
- the ineligible Grovlet never starts the service;
- the eligibility result/reason is observable in the structured control-plane view.

Use deterministic test conditions for MVP rather than depending on external networking. A LAN-reachability validator is the motivating production example, not a requirement for the public demo environment.

### Developer workflow and artifact
- Tasks 021-022: expose Grove's operational capabilities through Grove Shop itself rather than through a separately distributed generic CLI.
- The human-facing surface is a TUI embedded in the Grove Shop application binary.
- The TUI is backed by a structured action registry that can also be invoked non-interactively for CI, tests, scripts, and reproducible demos.
- The same action registry must support Grove built-in actions and application-specific actions registered by Grove Shop.
- Task 023: define one immutable artifact containing application code, Grove runtime, embedded TUI/operational actions, embedded UI assets, deployment metadata, and reserved config region.
- Task 024: implement customer config embedding/extraction and support distinct artifact identity when config differs.

### Upgrade and rollback
- Tasks 025-028: treat the complete artifact as the versioned deployment unit. Introduce candidate state, health gating, cutover, and rollback.
- A candidate with broken Inventory config must fail deterministically and cause Grove to return to the previous complete known-good artifact.
- Candidate placement must obey the same placement eligibility contract as normal deployment.

### Final lifecycle proof
- Tasks 029-031: automate the lifecycle in `DEMO_FLOW.md` using Go tests and the Grove test harness.
- Task 031 proves order success before the bad deployment and again after rollback.

### Final debugging proof
- Task 032 adds Delve/DAP as the final MVP task.
- Read `docs/adr/009-dap-debugging-interface.md`, `docs/developer-experience/debugging.md`, and `demo/DEBUGGING_DEMO.md` before implementing it.
- Run Grove Shop for this scenario as five Grovlets with one service per node: Web, Orders, Inventory, Payment, Shipping.
- Every service must execute in a dedicated worker process for the debugging scenario.
- The demo must attach **two independent debugger sessions concurrently** to workers on different nodes. The canonical targets are Orders on node-2 and Payment on node-4.
- Grove must resolve service -> node -> worker itself. The user must not find or supply PIDs, remote node addresses, or remote Delve ports.
- The first implementation is a discovery/tunnel layer around ordinary Delve DAP sessions. Do not build a stateful multi-process DAP multiplexer for the MVP.
- While a worker is intentionally paused by a debugger, Grove supervision must use explicit deterministic debug-session semantics so the breakpoint is not mistaken for an ordinary worker failure.
- Task 032 is not DONE until the exact human workflow documented in `demo/DEBUGGING_DEMO.md` has been executed successfully against the implementation in addition to the automated DAP E2E and `go test ./...`.

## UI implementation guidance
The Web UI is part of Grove Shop and must be served by a Grove-managed Web component.

The final page has two panes on one screen:
- Orders UI.
- Cluster Status.

The Cluster Status pane continuously polls a structured Grove read-model endpoint approximately every 500 ms to 1 second.

Use polling for MVP v1. Do not introduce WebSockets/SSE unless a later task explicitly changes the requirement.

The UI must be able to observe candidate rollout and rollback without controlling them.

When placement eligibility is exposed in the read model, the cluster view should preserve the distinction between `ineligible for this service` and generic node/service failure.

## Application console target
The headline human workflow is:

```bash
./bin/groveshop
```

The TUI should expose at least:

```text
GroveShop
├── Cluster
├── Services
├── Nodes
├── Deployments
├── Configuration
├── Logs
├── Debug
└── Application
```

Do not force the user through separate cluster-create, config-compile, config-embed, artifact-create, upgrade, status, or debugger-discovery command trees for the headline demo.

For deterministic automation and acceptance tests, the same application binary may invoke the underlying structured actions directly. The intended shape is:

```bash
./bin/groveshop action rollout.start --config configs/acme.yaml
./bin/groveshop action rollout.start --config configs/acme-broken.yaml
./bin/groveshop action cluster.status
```

The exact action names may evolve, but there must not be a separately required `grove` executable for normal application operation.

The debugging portion should be TUI-first:

```text
Services > orders > Instances > node-2 > Debug > Attach
Services > payment > Instances > node-4 > Debug > Attach
```

For automated acceptance, use the equivalent application-binary actions:

```bash
./bin/groveshop action debug.attach orders --listen 127.0.0.1:40000
./bin/groveshop action debug.attach payment --listen 127.0.0.1:40001
```

These are the Task 032 automation contract, not permission to skip validation. The exact final workflow and commands must be kept in `demo/DEBUGGING_DEMO.md` and tested as written before Task 032 is marked DONE.

## Developer-defined application actions
The same registry used by Grove's built-in TUI entries must allow Grove Shop to contribute application-specific operations, for example:

```text
Application
├── Seed demo orders
├── Generate load
└── Run integrity check
```

These actions should execute using the application's own packages, embedded configuration, credentials, and Grove connectivity. Do not create a second admin binary for them.

## Scope discipline
Do not implement general-purpose placement scoring, affinity/anti-affinity, or a sophisticated scheduler merely to support placement validation or the one-service-per-node debug demo. MVP only needs the hard eligibility boundary plus deterministic demo placement.

For debugging, do not implement DAP multiplexing, cross-service single-step semantics, global breakpoint fan-out, or an IDE-specific plugin. Two independent ordinary DAP sessions are enough for the MVP proof.

Do not add storefront complexity, authentication, external payment providers, databases, or frontend frameworks merely to make the sample feel realistic. The demo should remain deterministic, fast, and easy to E2E test.
