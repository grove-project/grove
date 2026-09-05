# Task 017 — Node failure detection E2E

Status: TODO
Depends on: 014, 016

## Goal
Establish permanent regression coverage for abrupt Grovlet loss.

## E2E
Start 3 real Grovlets, place app, verify flow, SIGKILL one hosting node, verify survivors detect loss and diagnostics identify affected placement.

## Out of scope
Do not recover the service yet.

## Done
`go test ./...` passes.