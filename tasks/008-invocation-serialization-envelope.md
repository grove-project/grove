# Task 008 — Invocation serialization envelope

Status: TODO
Depends on: 007

## Goal
Define the wire-independent request/response envelope for remote calls.

## Scope
Service ID, method ID, payload, response, structured error, request/correlation ID if needed; use the chosen MVP serialization consistently.

## Out of scope
Sockets, NATS, membership, routing.

## Tests
Round trips, malformed payloads, errors and compatibility behavior required by the chosen encoding.

## Done
`go test ./...` passes.