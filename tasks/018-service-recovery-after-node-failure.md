# Task 018 — Service recovery after node failure

Status: TODO
Depends on: 017

## Goal
Recover a stateless service after its hosting node dies.

## Scope
Simplest deterministic recovery policy; recreate Greeter on a healthy node.

## Out of scope
Stateful migration, snapshots, Firecracker, advanced scheduling, zero-downtime guarantee.

## E2E
Verify flow, kill Greeter's node, wait for new placement, verify flow resumes.

## Done
`go test ./...` passes.