# Task 012 — NATS cluster bootstrap

Status: TODO
Depends on: 011

## Goal
Allow Grovlets to join the same Grove control plane using explicit seed information and the embedded System NATS deployment.

## Scope
- Start/connect the embedded NATS topology required for a multi-Grovlet local cluster.
- Allow a joining Grovlet to use explicit seed information to reach the existing System NATS fabric.
- Make the bootstrap deterministic and test-friendly.
- Establish the shared System NATS substrate required by later JetStream/KV tasks.

## Architectural constraint
Do not create a parallel Grove-specific consensus or membership protocol beside NATS.

Do not implement custom Raft, etcd, or an unrelated leader-election mechanism. JetStream/KV will provide the replicated consensus-backed cluster-state layer in subsequent tasks.

## Out of scope
Cloud/DNS discovery, gossip optimization, edge connectivity, Grove membership semantics in KV, placement, and recovery.

## E2E
Start 3 real Grovlets using a deterministic seed topology and verify they all connect to the same System NATS control plane.

## Done
`go test ./...` passes.