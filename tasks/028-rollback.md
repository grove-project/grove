# Task 028 — Rollback

Status: TODO
Depends on: 027

## Goal
Return safely to the previous complete known-good artifact when a candidate fails validation/health, with rollback state coordinated through Grove's consensus-backed control plane.

## Required reading
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`
- `demo/UI.md`

## Scope
- Record rollback intent/progress and the active artifact identity in System NATS JetStream/KV.
- Ensure the previous known-good artifact remains available until the candidate is accepted.
- If the candidate fails validation, restore/retain the previous artifact as authoritative through the control state.
- Reconcile runtime placement/routing to the committed rollback state.
- Treat application code, embedded Web UI, and embedded customer configuration as one immutable deployment unit.
- Preserve a structured rollback reason suitable for CLI and Grove Shop Cluster Status display.

The headline E2E failure is the Grove Shop broken Inventory config from `demo/CONFIGURATION.md`. The candidate artifact contains `inventory.reservation_buffer < 0`, Inventory fails startup, Grove rejects the candidate, and the previous good artifact including its previous embedded config becomes/remains authoritative.

## Architectural constraint
Rollback, active-artifact identity, and rollout phase are durable control-plane metadata and must use JetStream/KV. Do not coordinate rollback through process-local memory, a separate metadata store, or custom consensus.

Do not implement rollback by mutating the candidate's embedded config in place. Rollback selects the previous immutable artifact.

## Out of scope
Reverse data migrations, chained historical migrations, canary policy, and hot config repair.

## E2E
Run healthy Artifact A plus intentionally unhealthy Artifact B with the broken Inventory configuration; attempt upgrade; verify:
1. B becomes a candidate,
2. Inventory failure is observed,
3. control state records candidate rejection/rollback,
4. authoritative active artifact resolves back to/remains A,
5. A's config identity is active again,
6. Grove Shop can complete an order after rollback.

## Done
`go test ./...` passes.