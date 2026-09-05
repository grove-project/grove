# Task 013 — Membership view

Status: TODO
Depends on: 012

## Goal
Expose a machine-readable cluster membership view from every Grovlet.

## Observable behavior
After convergence each node reports the same known node IDs/endpoints.

## Out of scope
Leader election, persistent history, failure recovery.

## E2E
Launch 3 nodes and assert membership convergence using condition waits only.

## Done
`go test ./...` passes.