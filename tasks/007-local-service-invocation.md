# Task 007 — Local service invocation

Status: TODO
Depends on: 006

## Goal
Allow Workflow to invoke Greeter through Grove while both run in one Grovlet.

## Scope
Local invocation, explicit IDs, handler execution, error propagation.

## Out of scope
Network transport, wire envelope, placement, remote routing.

## E2E
Start one real Grovlet via `grovetest`, run reference app, invoke Workflow, assert final response.

## Done
`go test ./...` passes.