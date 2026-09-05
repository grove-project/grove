# Task 027 — Traffic switch and upgrade

Status: TODO
Depends on: 026

## Goal
Perform the simplest health-gated N -> N+1 upgrade using durable Grove control state.

## Scope
- Represent the active application version and upgrade phase in System NATS JetStream/KV.
- Start N+1 while N remains authoritative.
- Wait for N+1 to become healthy.
- Commit the active-version/routing switch through the consensus-backed control state.
- Retire N only after the control-plane cutover is committed.
- Keep the strategy deterministic and minimal for the MVP.

## Architectural constraint
Upgrade progress and active-version ownership are durable control-plane facts. Store them in JetStream/KV; do not keep the authoritative upgrade state only in one Grovlet's memory or in a separate database.

## Out of scope
Canaries, percentages, data migration, rollout policy language, and custom consensus.

## E2E
Perform a successful N -> N+1 upgrade, verify the JetStream/KV-backed active-version state changes to N+1, and verify requests after cutover reach N+1.

## Done
`go test ./...` passes.