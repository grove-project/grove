# Task 025 — Deployment versions

Status: TODO
Depends on: 023

## Goal
Track application deployment versions explicitly.

## Scope
Control state distinguishes N from N+1 of the same application.

## Out of scope
Traffic switching, migration, rollback automation.

## E2E
Deploy N, verify state; introduce N+1, verify both represented distinctly.

## Done
`go test ./...` passes.