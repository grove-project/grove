# Task 029 — `grove test` command foundation

Status: TODO
Depends on: 022, 023

## Goal
Expose Grove's developer testing experience through `grove test` while Go tests remain the underlying automation mechanism.

## Scope
Orchestrate the reference application's Grove E2E flow and return deterministic exit status.

## Out of scope
Resilience matrix, SLA language, test generation.

## Tests
Exercise the real command from Go tests.

## Done
`go test ./...` passes.