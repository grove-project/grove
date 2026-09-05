# Task 019 — Desired deployment state

Status: TODO
Depends on: 018

## Goal
Separate what Grove intends to run from observed runtime state.

## Scope
Minimum desired application/components/version/placement model; reconciliation drives observed state toward desired state.

## Out of scope
Durable storage, user-facing declarative language, rollout strategies.

## Tests
Unit-test reconciliation. E2E kill a managed component and prove reconciliation restores desired state.

## Done
`go test ./...` passes.