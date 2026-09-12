# Plan: Track deployment artifacts and rollout identity

## Goal

Complete Task 025 by recording immutable configured artifacts and rollout
generations in replicated System NATS JetStream/KV state, exposing consistent
watcher-derived views from every Grovlet, and tying desired deployment and
service placement records to exact artifact identities. Stop before transfer,
candidate launch, bootstrap negotiation, handoff, or rollback behavior.

## Context

Tasks 019 and 020 established durable desired deployment and placement state.
Tasks 023 and 024 established verified application, code, configuration, and
final artifact identities. Task 025 connects those contracts in control state;
Task 026 remains responsible for actually launching N and N+1 side by side.

### Key Files

- `tasks/025-deployment-versions.md` — current acceptance contract.
- `internal/systemnats/deployment.go` — new authoritative artifact and rollout
  records, replicated KV watcher, validation, writes, and request/command API.
- `internal/systemnats/desired.go` — desired application intent gains an exact
  artifact reference.
- `internal/systemnats/placement.go` — each service placement gains the exact
  artifact identity hosting that service.
- `cmd/grovlet/main.go` — every clustered Grovlet serves and observes the
  deployment-control view while propagating its running artifact identity into
  placement records.
- `cmd/grovlet/recovery.go` — recovery preserves the placed artifact identity;
  this task does not change software during recovery.
- `cmd/grove/deployment_test.go` — real configured-artifact and multi-Grovlet
  proof for current N and config-only candidate N+1.

### Decisions Made

- Use one `GROVE_DEPLOYMENTS` JetStream/KV bucket with separate
  `artifacts.<sha256>` and `rollouts.<application>` keys. Artifact records stay
  independently addressable while one watcher exposes a coherent, sorted local
  view. JetStream's built-in replication remains the only consensus layer.
- Artifact records contain application/code version, code digest, config
  revision/digest, final artifact digest, cluster identity, and optional node
  class/zone. They describe metadata only; configuration bytes remain solely in
  the immutable artifact.
- Rollout records contain an explicit rollout ID, monotonically increasing
  generation, current and optional candidate artifact digests, overall phase,
  and per-node current/candidate progress. Writes verify that every referenced
  artifact exists and belongs to the same application and cluster.
- Artifact writes are immutable/idempotent by final digest. Rollout writes use
  KV revision comparison and accept only the next generation (or an identical
  retry), preventing two writers from silently replacing one rollout identity.
- Add `artifact_digest` to desired deployment and placement records and require
  it during validation. Existing runtime recovery moves the same exact artifact
  placement; candidate placement remains out of scope until side-by-side work.

## Sub-Tasks

- [x] 1. Add durable deployment-control records.
  **Context:** Define immutable artifact, rollout, and per-node progress types;
  canonical digest/key validation; deterministic snapshots; replicated bucket
  watcher; idempotent artifact writes; generation-checked rollout writes; and
  System NATS read/write endpoints.

- [x] 2. Require artifact identity in desired state and placement.
  **Context:** Extend validation, copying, equality expectations, and recovery
  so every deployment/placement identifies the immutable artifact it refers to,
  rather than relying on a diagnostic version label.

- [x] 3. Run deployment observers in every clustered Grovlet.
  **Context:** Serve and run the watcher-derived deployment view with existing
  membership/desired/placement lifecycle management. Propagate the inspected
  running artifact digest into initial Grove Shop placements without creating
  candidate execution behavior.

- [x] 4. Prove current and config-only candidate control state.
  **Context:** Build two configured variants from one code image, run N on a
  real three-Grovlet cluster, publish current generation N, introduce N+1 as a
  pending candidate, and verify exact artifact/config/code identities plus
  per-node progress consistently from every Grovlet and directly in the
  replicated KV bucket.

- [x] 5. Verify and close Task 025.
  **Context:** Run formatting, diff checks, focused repetitions, vet, the full
  uncached suite, and full race suite; mark DONE only after every earlier E2E
  remains green, then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 024 passed focused repetitions, full uncached tests, and the
  full race suite and was pushed to `main` at `61bebca`.
- 2026-09-12: Kept deployment metadata separate from embedded configuration.
  Task 025 records identities and intent only; stable bootstrap negotiation and
  side-by-side candidate processes remain owned by Tasks 026–028.
- 2026-09-12: Corrected runtime artifact identity to inspect the complete
  executable rather than only the embedded manifest/config envelope. Runtime
  placement and readiness now match offline `grove config inspect` exactly.
- 2026-09-12: Focused System NATS, CLI, and complete Grovlet package tests pass,
  including the real config-only N to N+1 control-state E2E and every earlier
  distributed Grovlet scenario.
- 2026-09-12: Task 025 passed five consecutive real rollout-state E2Es,
  race-focused checks, `go vet ./...`, `go test -count=1 ./...`, and
  `go test -race -count=1 ./...`; marked the task DONE.
