# Task 002 — Grovlet executable lifecycle

Status: TODO
Depends on: 001

## Goal
Make `grovlet` a real long-running process with deterministic startup/readiness/shutdown.

## Scope
Runtime-dir flag, machine-readable readiness, graceful SIGTERM, clear startup errors.

## Out of scope
Membership, services, peer transport, daemonization.

## Tests
Unit-test validation/shutdown; integration-test real process readiness and termination. Direct `os/exec` is allowed only until task 003 provides the harness.

## Done
`go test ./...` passes.