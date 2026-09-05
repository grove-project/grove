# Task 012 — Static cluster join

Status: TODO
Depends on: 011

## Goal
Allow Grovlets to join using explicit seed information.

## Scope
Simplest explicit join protocol sufficient for local MVP testing.

## Out of scope
Raft, cloud/DNS discovery, gossip optimization, edge connectivity.

## E2E
Start 3 real Grovlets with deterministic seed topology and verify they become mutually known.

## Done
`go test ./...` passes.