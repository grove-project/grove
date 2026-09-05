# Task 020 — Durable control state and cluster restart

Status: TODO
Depends on: 019

## Goal
Persist enough control state to reconstruct desired deployment after all Grovlets restart.

## Scope
Only persistence required by current MVP behavior.

## Out of scope
Cross-version schema migration, remote object storage, production DR.

## E2E
Deploy app, stop all Grovlets, restart from same state dirs, wait for deployment reconstruction, verify flow.

## Done
`go test ./...` passes.