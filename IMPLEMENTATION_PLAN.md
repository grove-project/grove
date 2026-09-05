# Grove MVP Implementation Plan

## Purpose
This plan is designed for incremental execution by Codex/Claude. Grove is built as small vertical capabilities; every task adds observable behavior plus permanent automated regression coverage.

## MVP thesis
A developer writes a normal Go app using a small explicit Grove SDK, tests it locally, deploys the same artifact to a multi-node Grove cluster, Grove places/monitors components, recovers after node failure, survives cluster restart, and upgrades/rolls back safely.

## Core rules
- Explicit distribution: explicit service registration and invocation; stable service/method IDs.
- No required generated code and no interface-heavy RPC abstraction.
- Same application-facing invocation API for local and remote calls.
- Distributed E2E = multiple real Grovlet processes on one host using real transport.
- All acceptance via Go testing framework and dedicated `grovetest` harness.
- Build required binaries once per test package invocation where practical.
- No fixed sleeps; use condition waits with bounded timeouts and diagnostics.
- Task N may not break tests from tasks 1..N-1.

## Accepted control-plane architecture
The MVP must follow Grove's accepted NATS architecture:

- Grove embeds NATS as the system communication and coordination substrate.
- System NATS carries Grove control messaging.
- JetStream/KV is the authoritative replicated cluster-state layer.
- Grove relies on JetStream's internal Raft implementation for replicated consensus and must not implement a separate Raft/etcd control store.
- Grove reconciliation and cluster semantics are built on top of System NATS + JetStream/KV.
- Membership, desired state, placement, ownership, versions, and durable control-plane metadata are represented in System NATS JetStream/KV as the corresponding tasks introduce them.
- RPC, heartbeats, commands, and other transient control traffic use NATS messaging where appropriate.
- System NATS and Data NATS are logically separate planes even if the MVP initially runs them in the same physical NATS server process.

## Reference app
Keep one deterministic reference app throughout the MVP: `Greeter` plus `Workflow`, where Workflow invokes Greeter. Add state only when a later task needs it.

## Phases
A. Foundation (001-005): repository, Grovlet, process/cluster harness, reference app.
B. Runtime (006-010): registry, local invocation, envelope, embedded System NATS transport, cross-node invocation.
C. Cluster awareness (011-015): identity, NATS-based cluster bootstrap, JetStream/KV membership, NATS heartbeats, explicit placement in replicated control state.
D. Recovery/persistence (016-020): lifecycle, failure detection, recovery, desired state in JetStream/KV, JetStream-backed restart recovery.
E. Developer workflow (021-024): CLI, CLI E2E, deployment artifact, embedded config.
F. Upgrades (025-028): versions, side-by-side, cutover, rollback.
G. Resilience/MVP proof (029-031): `grove test`, resilience injection, final lifecycle E2E.

## Outside MVP
Firecracker/live migration, Kubernetes integration, edge-specific connectivity, DAP proxy, advanced scheduling hints, durable execution, WASM plugins, migration chains, sophisticated hot config, production multi-region control plane, advanced observability backend, and any separate Grove-owned Raft/etcd consensus implementation.

## Execution prompt
> Read AGENTS.md, IMPLEMENTATION_PLAN.md, and tasks/NNN-*.md. Implement only that task. Run all required tests. Do not proceed to the next task. Report changes, tests/results, deviations, and architectural issues.

## Status
Each task starts `Status: TODO`; change to `DONE` only after all acceptance criteria pass.

## Task index
001 repository skeleton; 002 Grovlet lifecycle; 003 process harness; 004 local multi-Grovlet harness; 005 reference app; 006 service registry; 007 local invocation; 008 serialization envelope; 009 embedded System NATS transport; 010 cross-node invocation; 011 node identity; 012 NATS cluster bootstrap; 013 JetStream/KV membership; 014 NATS heartbeats/health; 015 explicit placement in control state; 016 component lifecycle; 017 node-failure E2E; 018 service recovery; 019 desired state in JetStream/KV; 020 JetStream-backed restart recovery; 021 CLI; 022 CLI E2E; 023 deployment artifact; 024 embedded config; 025 versions; 026 N/N+1 side-by-side; 027 traffic switch; 028 rollback; 029 `grove test`; 030 resilience scenario; 031 final MVP E2E.