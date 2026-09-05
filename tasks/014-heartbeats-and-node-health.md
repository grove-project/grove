# Task 014 — Heartbeats and node health

Status: TODO
Depends on: 013

## Goal
Track peer liveness with bounded failure detection.

## Scope
Heartbeat exchange, last-seen tracking, health states and configurable detection timing.

## Out of scope
Service relocation, split-brain resolution, quorum semantics.

## E2E
Start 3 nodes, kill one, condition-wait until both survivors report it unavailable. No fixed sleeps.

## Done
`go test ./...` passes.