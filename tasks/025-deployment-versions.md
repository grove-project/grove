# Task 025 — Deployment artifacts and rollout identity in control state

Status: DONE
Depends on: 023, 024, 019

## Goal
Track configured deployment artifacts and rollout generations explicitly in Grove's authoritative replicated control state.

## Scope
- Extend JetStream/KV control state so the same application code can have distinct configured artifact records.
- Track code digest, config revision/digest, final artifact digest, cluster identity, and configured node class/zone where applicable.
- Represent current N and candidate N+1 artifacts distinctly.
- Keep artifact identity stable and visible to all Grovlets.
- Ensure placement/deployment records reference an explicit artifact identity, not only an application version string.
- Add durable rollout identity/state sufficient for later tasks to report current/candidate artifact and per-node progress.

Two artifacts with identical code but different embedded configuration are distinct deployment artifacts and must be independently visible in control state.

## Architectural constraint
Deployment/artifact/rollout metadata is durable Grove control state and belongs in System NATS JetStream/KV. Do not introduce a separate version registry or metadata database.

Configuration itself remains embedded in the artifact and immutable at runtime. Control state describes which immutable artifact is current/candidate; it is not a mutable configuration store.

## Out of scope
Artifact transfer, side-by-side process launch, traffic/ownership handoff, migration, rollback automation, external artifact registry integration, and custom consensus.

## E2E
Deploy N and verify its JetStream/KV-backed state; introduce N+1 and verify both configured artifacts are represented distinctly and consistently from multiple Grovlets. Include a config-only N→N+1 case where code digest stays equal but config/artifact digests differ.

## Done
`go test ./...` passes.
