# Task 022 — CLI-driven local cluster E2E

Status: TODO
Depends on: 021

## Goal
Prove the operator lifecycle through the real CLI.

## E2E
Go test launches 3 Grovlets, invokes real `grove`, inspects nodes/placement, stops/starts a component through CLI, verifies reference app works.

## Out of scope
Deploy and upgrade commands.

## Done
`go test ./...` passes.