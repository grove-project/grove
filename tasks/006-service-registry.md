# Task 006 — Service registry

Status: TODO
Depends on: 005

## Goal
Introduce explicit service/method registration using stable IDs.

## Scope
Explicit handler registration/resolution; reject duplicates and unknown IDs. No generated code or required Go interfaces.

## Out of scope
Serialization, transport, placement, reflection-driven generic dispatch unless strictly necessary.

## Tests
Registration, duplicate service/method, unknown service/method.

## Done
`go test ./...` passes.