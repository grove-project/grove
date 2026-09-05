# Task 030 — Resilience scenario execution

Status: TODO
Depends on: 018, 029

## Goal
Run one controlled resilience action while a developer-defined E2E flow executes.

## Scope
MVP action: kill the node hosting a selected stateless service; wait for Grove recovery; rerun functional flow.

## Out of scope
Full resilience matrix, SLA enforcement, network partitions, disk corruption, CPU throttling.

## E2E
`grove test` proves flow before failure, injects node death, waits for recovery, proves flow again.

## Done
`go test ./...` passes.