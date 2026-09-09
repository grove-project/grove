# Grove MVP Implementation Plan

## Purpose
This plan is designed for incremental execution by Codex/Claude. Grove is built as small vertical capabilities; every task adds observable behavior plus permanent automated regression coverage.

## MVP thesis
A developer writes a normal Go app using a small explicit Grove SDK, tests it locally, deploys an immutable configured binary to a multi-node Grove cluster, Grove places/monitors components, recovers after node failure, survives cluster restart, and rolls software or configuration changes out safely through its existing mesh.

The permanent MVP reference application is **Grove Shop**. Its exact demo contract lives under `demo/` and must be treated as a first-class implementation target rather than a late-stage sample.

## Normative developer-facing SDK
The MVP SDK contract is defined under `sdk/` and must be treated as normative implementation guidance:
- `sdk/README.md`
- `sdk/DESIGN_PRINCIPLES.md`
- `sdk/SERVICE_MODEL.md`
- `sdk/INVOCATION.md`
- `sdk/SERIALIZATION.md`
- `sdk/EXAMPLE.md`

The SDK must keep Grove explicit and Go-native: ordinary business types, explicit registration, explicit stable service/method IDs, no required interfaces, no generated stubs/codegen, no reflection-driven dispatch, Gob behind small Grove helpers, and one Grove invocation path whose routing may resolve locally or remotely.

Service placement requirements are developer-defined through the SDK when needed. A service may provide an optional placement validator that each Grovlet evaluates locally. No validator means eligible everywhere. A failed validator is a hard constraint.

Placement validation may use immutable Grovlet facts derived from embedded configuration (for example `node.zone`) and live node-local environmental checks (for example endpoint reachability). Grove does not infer authoritative node class/zone from network heuristics.

## Immutable configuration and artifact model
- The Grove binary contains a reserved embedded configuration section.
- Cluster/customer/environment/site/node-class values are embedded into that section.
- A running artifact never mutates its configuration.
- Different configured variants can contain identical executable code; for example cloud and edge variants can differ only by embedded `node.zone`.
- Code digest, config digest/revision, and final artifact digest are distinct identities.
- A configuration change always produces another immutable artifact and rollout event, even when code is unchanged.
- The CLI can inspect/extract artifact configuration and inspect the effective configuration of running nodes; it does not provide normal live configuration mutation.

## Core rules
- Explicit distribution: explicit service registration and invocation; stable service/method IDs.
- No required generated code and no interface-heavy RPC abstraction.
- Same application-facing Grove invocation API for local and remote calls.
- Business services remain directly unit-testable as ordinary Go code.
- Placement validation is optional and developer-supplied through the SDK.
- No placement validator = service eligible on every Grovlet.
- Placement validation determines where a service **can** run; scheduling chooses where it **should** run among eligible nodes.
- A Grovlet must never start/restart a service whose placement validation fails.
- Runtime configuration is immutable; configuration changes are deployments.
- The current Grove cluster bootstraps successor artifacts through the existing Grove mesh.
- Binary handoff is side-by-side: old process remains authoritative until the successor joins and proves healthy.
- Distributed E2E = multiple real Grovlet processes on one host using real transport.
- All acceptance via Go testing framework and dedicated `grovetest` harness.
- No fixed sleeps; use condition waits with bounded timeouts and diagnostics.
- Task N may not break tests from tasks 1..N-1.

## Accepted control-plane architecture
- Grove embeds NATS as the system communication and coordination substrate.
- System NATS carries Grove control messaging.
- JetStream/KV is the authoritative replicated cluster-state layer.
- Grove relies on JetStream's internal Raft and must not implement a separate Raft/etcd control store.
- Membership, desired state, placement, ownership, artifact identities, rollout generations/phases, versions, and durable control-plane metadata are represented in System NATS JetStream/KV as tasks introduce them.
- Placement eligibility is evaluated locally by Grovlets; enough result/reason is surfaced for placement decisions and diagnostics.
- Rollout state is durable and observable from any healthy CLI connection.
- System NATS and Data NATS are logically separate planes even if the MVP initially shares one physical NATS process.

## Binary rollout and handoff contract
Once a cluster exists, normal Grove rollout uses the existing cluster as the deployment transport/bootstrap path.

Per target node:

```text
current Grovlet
   -> obtain successor artifact
   -> verify artifact + embedded config
   -> check cluster/node-class compatibility
   -> persist candidate
   -> start candidate beside current process
   -> candidate joins existing mesh
   -> candidate becomes healthy
   -> ownership/service handoff
   -> old process exits
```

If any pre-handoff step fails, the candidate is rejected/stopped and the old process remains authoritative. Normal handoff preserves configured node class (`cloud-v1 -> cloud-v2`, `edge-v1 -> edge-v2`). Reclassifying a machine is explicit reprovisioning, not an incidental rollout.

The CLI should expose phases such as `pending`, `transferring`, `verifying`, `starting`, `joining`, `handoff`, `healthy`, and `failed`, with actionable per-node failure reasons.

## Reference app
Use one deterministic reference app throughout the MVP: **Grove Shop** with `Web`, `Orders`, `Inventory`, `Payment`, and `Shipping`.

The Web component serves embedded HTML/CSS/JavaScript from the same Grove deployment. The UI contains an Orders pane and Grove Cluster Status pane that continuously polls nodes, placement, health, current/candidate artifacts, rollout/handoff progress, and rollback.

The demo must prove embedded configuration. A good config produces a healthy deployment. A second artifact built from the same application code with a deliberately invalid Inventory config must cause the candidate to become unhealthy. Grove must detect this and retain/restore the previous complete known-good artifact/config.

Placement-validation semantics must be covered permanently with deterministic per-node conditions: unrestricted, passing, failing, mixed eligibility, and refusal to start on an ineligible Grovlet.

Read detailed contracts before demo-facing work under `demo/` plus `docs/developer-experience/deployment.md` and `docs/cli/rollouts.md`.

## Phases
A. Foundation (001-005): repository, Grovlet, process/cluster harness, Grove Shop.
B. Runtime + SDK (006-010): registry, local invocation, Gob envelope, System NATS transport, cross-node invocation.
C. Cluster awareness and placement (011-015): identity, bootstrap, JetStream/KV membership, heartbeats, local eligibility validation, explicit placement.
D. Recovery/persistence (016-020): eligibility-enforcing lifecycle, failure detection/recovery, desired state, restart recovery.
E. Developer workflow (021-024): minimal CLI, CLI E2E, immutable deployment artifact, embedded customer/cluster/node configuration.
F. Self-hosted rollouts (025-028): configured artifact/rollout identity, side-by-side successor execution using the existing Grove mesh, health-gated ownership handoff, automatic rollback/retention of previous artifact. Candidate placement obeys the same eligibility rules.
G. Resilience/MVP proof (029-031): `grove test`, resilience injection, final Grove Shop lifecycle E2E.

## Outside MVP
Firecracker/live migration, Kubernetes integration, edge-specific transport optimization, DAP/debugger proxy, advanced scheduling hints, durable execution, WASM plugins, migration chains, production multi-region control plane, advanced observability backend, generated RPC clients/stubs, and any separate Grove-owned Raft/etcd consensus implementation.

General-purpose scheduling/scoring, affinity/anti-affinity, and dynamic relocation after changing placement eligibility remain outside MVP. Live runtime configuration mutation is intentionally not a future hot-config path in the normal Grove model; configuration changes remain artifact rollouts.

Debugger/DAP integration is a natural MVP v2 demo extension and must not expand MVP v1 scope.

## Execution prompt
> Read AGENTS.md, IMPLEMENTATION_PLAN.md, the relevant `sdk/*.md`, `demo/*.md`, deployment/rollout docs, and tasks/NNN-*.md. Implement only that task. Run all required tests. Do not proceed to the next task. Report changes, tests/results, deviations, and architectural issues.

## Status
Each task starts `Status: TODO`; change to `DONE` only after all acceptance criteria pass.

## Task index
001 repository skeleton; 002 Grovlet lifecycle; 003 process harness; 004 local multi-Grovlet harness; 005 Grove Shop reference app; 006 service registry; 007 local invocation; 008 serialization envelope; 009 embedded System NATS transport; 010 cross-node invocation; 011 node identity; 012 NATS cluster bootstrap; 013 JetStream/KV membership; 014 NATS heartbeats/health; 015 placement validation + explicit placement; 016 eligibility-enforcing lifecycle; 017 node-failure E2E; 018 service recovery; 019 desired state; 020 restart recovery; 021 CLI; 022 CLI E2E; 023 deployment artifact; 024 immutable embedded customer/cluster/node config; 025 configured artifact + rollout identity; 026 N/N+1 side-by-side successor execution; 027 health-gated binary/ownership handoff; 028 rollback; 029 `grove test`; 030 resilience scenario; 031 final Grove Shop MVP E2E.
