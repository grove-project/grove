# Task 014 — NATS heartbeats and node health

Status: TODO
Depends on: 013

## Goal
Track distributed Grovlet liveness using System NATS while keeping durable cluster facts in the JetStream/KV control plane.

## Scope
- Exchange node heartbeats over System NATS subjects.
- Track last-seen timestamps and health transitions.
- Expose healthy/unavailable state through Grove's machine-readable cluster view.
- Keep heartbeat messages ephemeral; do not turn every heartbeat into a KV write.
- When health becomes authoritative control metadata, represent the resulting health/ownership state through the System NATS control-state model rather than a separate database.

## Architectural constraint
Use NATS messaging for transient liveness signals and JetStream/KV for consensus-backed control metadata. Do not implement a parallel heartbeat transport or separate consensus store.

## Out of scope
Service relocation, split-brain policy, advanced quorum semantics, and a custom Raft implementation.

## E2E
Start 3 nodes, kill one, and condition-wait until both survivors report it unavailable. No fixed sleeps.

## Done
`go test ./...` passes.