# Task 026 — Side-by-side N and N+1

Status: TODO
Depends on: 025

## Goal
Run two versions of one application simultaneously.

## Observable behavior
Both versions can be healthy and explicitly invoked.

## Out of scope
Automatic cutover, schema migration, general mixed-version compatibility.

## E2E
Run reference app N and N+1 side-by-side and invoke each explicitly.

## Done
`go test ./...` passes.