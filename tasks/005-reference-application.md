# Task 005 — Reference application

Status: TODO
Depends on: 001

## Goal
Create the deterministic app reused by all later E2Es.

## Scope
`Greeter` and `Workflow`; Workflow calls Greeter through a plain abstraction that can later use Grove without changing business logic.

## Out of scope
Remote invocation, registry, persistence, deployment.

## Tests
Unit-test the reference app independently of Grove.

## Done
`go test ./...` passes.