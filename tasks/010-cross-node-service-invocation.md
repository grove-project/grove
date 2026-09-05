# Task 010 — Cross-node service invocation

Status: TODO
Depends on: 007, 009

## Goal
Workflow on Grovlet A invokes Greeter on Grovlet B through the same application-facing API as local invocation.

## Scope
Explicit destination routing, serialization, transport, destination dispatch, response/error propagation.

## Out of scope
Automatic placement/discovery, retries, failover, Raft.

## E2E
Start 2 real Grovlets; Workflow on A, Greeter on B; invoke Workflow and assert cross-process response.

## Done
`go test ./...` passes.