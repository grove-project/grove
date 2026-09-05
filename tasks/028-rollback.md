# Task 028 — Rollback

Status: TODO
Depends on: 027

## Goal
Return safely to N when N+1 fails validation/health.

## Scope
Runtime/deployment version rollback required by MVP.

## Out of scope
Reverse data migrations and chained historical migrations.

## E2E
Healthy N + intentionally unhealthy N+1; attempt upgrade; verify rollback and continued service from N.

## Done
`go test ./...` passes.