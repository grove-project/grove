# Task 003 — Process test harness

Status: TODO
Depends on: 002

## Goal
Create reusable Go `grovetest` support for building and launching the real Grovlet binary.

## Scope
BuildGrovlet, StartNode, WaitReady, Stop, Kill, Restart, Logs, TempDir, Cleanup. Build once per test-package invocation where practical.

## Out of scope
Multi-node semantics, membership, deployment, network faults.

## Tests
Prove startup, readiness, graceful stop, forced kill, restart, cleanup, startup-failure diagnostics. No fixed sleeps.

## Done
`go test ./...` passes.