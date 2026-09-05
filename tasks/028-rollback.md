# Task 028 — Rollback

Status: TODO
Depends on: 027

## Goal
Return safely to N when N+1 fails validation/health, with rollback state coordinated through Grove's consensus-backed control plane.

## Scope
- Record rollback intent/progress and the active application version in System NATS JetStream/KV.
- Ensure N remains available until N+1 is accepted.
- If N+1 fails validation, restore/retain N as the authoritative active version through the control state.
- Reconcile runtime placement/routing to the committed rollback state.

## Architectural constraint
Rollback and active-version state are durable control-plane metadata and must use JetStream/KV. Do not implement rollback coordination through process-local memory, a separate metadata store, or custom consensus.

## Out of scope
Reverse data migrations and chained historical migrations.

## E2E
Run healthy N plus intentionally unhealthy N+1, attempt upgrade, verify JetStream/KV control state resolves back to N, and verify continued service from N.

## Done
`go test ./...` passes.