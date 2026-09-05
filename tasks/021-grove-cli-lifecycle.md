# Task 021 — Grove CLI lifecycle

Status: TODO
Depends on: 013, 016

## Goal
Add the smallest useful `grove` CLI against public Grove control APIs.

## Scope
Commands for status, nodes, components and component start/stop where appropriate.

## Out of scope
Deployment packaging, upgrades, TUI.

## Tests
Unit-test parsing/errors. E2E invokes the real CLI binary against a `grovetest` cluster.

## Done
`go test ./...` passes.