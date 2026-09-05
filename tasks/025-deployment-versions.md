# Task 025 — Deployment versions in control state

Status: TODO
Depends on: 023, 019

## Goal
Track application deployment versions explicitly in Grove's authoritative replicated control state.

## Scope
- Extend the JetStream/KV control-state model so the same application can have distinct version N and N+1 records.
- Keep version identity stable and visible to all Grovlets.
- Ensure placement/deployment records reference an explicit application version.

## Architectural constraint
Deployment/version metadata is durable Grove control state and belongs in System NATS JetStream/KV. Do not introduce a separate version registry or metadata database.

## Out of scope
Traffic switching, migration, rollback automation, artifact registry integration, and custom consensus.

## E2E
Deploy N and verify its JetStream/KV-backed state; introduce N+1 and verify both versions are represented distinctly and consistently from multiple Grovlets.

## Done
`go test ./...` passes.