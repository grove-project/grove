# Task 009 — Node transport

Status: TODO
Depends on: 004, 008

## Goal
Provide real inter-process request/response transport between two Grovlets.

## Scope
Smallest production-shaped transport that can later work unchanged across hosts.

## Out of scope
Discovery, retries, load balancing, placement, Raft.

## Tests
Integration-test endpoints. E2E starts 2 real Grovlets and exchanges a transport-level request.

## Done
`go test ./...` passes.