# Task 027 — Traffic switch and upgrade

Status: TODO
Depends on: 026

## Goal
Perform the simplest health-gated N -> N+1 upgrade.

## Scope
Start N+1, wait healthy, switch active routing, then retire N using one deterministic strategy.

## Out of scope
Canaries, percentages, data migration, rollout policy language.

## E2E
Successful N -> N+1 upgrade; requests after cutover must reach N+1.

## Done
`go test ./...` passes.