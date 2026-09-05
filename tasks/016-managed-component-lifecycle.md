# Task 016 — Managed component lifecycle

Status: TODO
Depends on: 015

## Goal
Make Grovlet responsible for starting, stopping and reporting hosted component lifecycle.

## Scope
States such as starting, healthy, stopping, stopped, failed.

## Out of scope
Automatic recovery, persistence, rolling upgrades.

## E2E
Start component, verify healthy, stop, verify stopped, start again, verify app flow.

## Done
`go test ./...` passes.