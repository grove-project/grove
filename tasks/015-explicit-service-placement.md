# Task 015 — Explicit service placement in control state

Status: TODO
Depends on: 010, 013

## Goal
Run a named service on a specifically selected Grovlet and represent that placement in Grove's authoritative replicated control state.

## Scope
- Add the minimum placement record model.
- Store authoritative placement metadata in System NATS JetStream/KV.
- Allow tests/control logic to request Workflow on node A and Greeter on node B.
- Make all Grovlets observe the same placement state through the replicated control plane.
- Use the placement state to route the reference application correctly.

## Architectural constraint
Placement is control-plane metadata and belongs in JetStream/KV. Do not create a separate local authoritative placement database or custom consensus layer.

## Out of scope
Scheduler, scoring, capacity balancing, affinity, automatic relocation, advanced ownership arbitration, and custom Raft.

## E2E
Place Workflow on A and Greeter on B, verify the placement records through the control-plane view on multiple Grovlets, then execute the cross-node flow successfully.

## Done
`go test ./...` passes.