# Task 015 — Explicit service placement

Status: TODO
Depends on: 010, 013

## Goal
Run a named service on a specifically selected Grovlet and expose placement state.

## Scope
Explicit placement only.

## Out of scope
Scheduler, scoring, capacity balancing, affinity, automatic relocation.

## E2E
Place Workflow on A and Greeter on B, verify placement state, execute cross-node flow.

## Done
`go test ./...` passes.