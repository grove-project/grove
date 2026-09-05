# Task 018 — Service recovery after node failure

Status: TODO
Depends on: 017

## Goal
Recover a stateless service after its hosting node dies by reconciling authoritative Grove control state through System NATS + JetStream/KV.

## Scope
- Detect that the node hosting Greeter is unavailable.
- Use the current desired/placement control state to select a healthy replacement node with the simplest deterministic MVP policy.
- Update authoritative placement/ownership metadata through JetStream/KV rather than a local-only map.
- Start Greeter on the replacement node.
- Restore successful Workflow -> Greeter execution.

## Architectural constraint
Recovery decisions must converge through the consensus-backed System NATS control plane. Do not introduce a separate recovery database, custom consensus protocol, or Grove-owned Raft implementation.

## Out of scope
Stateful migration, snapshots, Firecracker, advanced scheduling, zero-downtime guarantee, and sophisticated leader/reconciliation distribution.

## E2E
Verify flow, kill Greeter's node, wait for the old node to become unavailable, wait for a new JetStream/KV-backed placement to appear, then verify the application flow resumes.

## Done
`go test ./...` passes.