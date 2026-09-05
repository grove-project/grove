# Task 019 — Desired deployment state in JetStream/KV

Status: TODO
Depends on: 018

## Goal
Separate what Grove intends to run from observed runtime state and make desired deployment state authoritative through System NATS JetStream/KV.

## Scope
- Define the minimum desired application/component/version/placement model.
- Store desired deployment state in JetStream/KV.
- Observe/watch desired-state changes through the control plane.
- Implement reconciliation that compares desired state with observed state and drives the cluster toward the desired state.
- Keep observed process/runtime facts distinct from desired intent.

## Architectural constraint
JetStream/KV is the consensus-backed source of truth for desired cluster state. Do not add a second durable control database or custom Raft implementation.

## Out of scope
User-facing declarative language, rollout strategies, migration chains, and advanced reconciliation leadership/distribution.

## Tests
Unit-test reconciliation decisions and KV encoding/watch behavior.

## E2E
Write desired deployment state through the Grove control API, verify it is visible from multiple Grovlets, kill a managed component, and prove reconciliation restores the desired state.

## Done
`go test ./...` passes.