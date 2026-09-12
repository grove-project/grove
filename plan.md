# Plan: Run current and candidate artifacts side by side

## Goal

Complete Task 026 by defining a tiny stable bootstrap hello/negotiation
boundary and proving that current N and candidate N+1 Grove Shop artifacts can
run healthy at the same time on the existing System NATS mesh and be invoked
explicitly. Do not switch placement/traffic, retire N, migrate state, or claim
general mixed-version application compatibility.

## Context

Task 025 records exact current/candidate artifact and rollout identities in
replicated control state. Task 026 makes simultaneous execution possible and
introduces the stable compatibility check required before later ownership
handoff. Task 027 remains responsible for health-gated authoritative cutover.

### Key Files

- `tasks/026-side-by-side-n-and-n-plus-1.md` — current acceptance contract.
- `docs/architecture/bootstrap-and-binary-handoff.md` — stable bootstrap ABI
  constraints.
- `internal/bootstrap/bootstrap.go` — versioned, language-independent JSON
  hello envelope, artifact/process identity, capabilities, validation, and
  negotiation.
- `cmd/grovlet/bootstrap.go` — private target-binary bootstrap hello entrypoint
  populated from the exact running artifact.
- `cmd/grove/bootstrap.go` — invokes current and candidate target binaries and
  negotiates their common bootstrap protocol/capabilities.
- `cmd/grove/side_by_side_test.go` — real N/N+1 processes and explicit
  invocation proof over production-shaped System NATS transport.

### Decisions Made

- Use a conservative JSON envelope with explicit protocol version, message
  type, message version, and payload. Do not use Gob or evolving SDK/runtime
  structs for the bootstrap wire boundary.
- Bootstrap hello advertises supported bootstrap versions, capabilities,
  application/code/config/artifact identity, cluster identity, and optional
  node class/zone. Negotiation requires a common protocol plus side-by-side
  process and readiness capabilities and rejects application, cluster, or node
  class/zone mismatches before launch/handoff work.
- Add a private `bootstrap-hello` target entrypoint. The current CLI-side
  runtime invokes both exact binaries and computes their compatible
  intersection; no application SDK compatibility is inferred from version
  labels.
- Run candidate Orders and Inventory as distinct real Grovlet processes on the
  current cluster's System NATS plane with candidate-specific subjects. Current
  calls use authoritative placement; candidate calls use an explicit candidate
  subject. No placement or active-artifact switch occurs in this task.
- Use two config-only artifact revisions for N and N+1 so the E2E can prove
  which artifact handled each request deterministically while retaining the
  same executable code identity.

## Sub-Tasks

- [x] 1. Define and test the stable bootstrap hello contract.
  **Context:** Add the versioned envelope and hello identity, deterministic
  capability negotiation, compatibility errors, unknown-optional-field
  tolerance, and malformed/incompatible tests without importing SDK models.

- [x] 2. Expose target-owned bootstrap identity.
  **Context:** Add the private Grovlet hello command using full executable and
  embedded configuration inspection. Add CLI-side target invocation and prove
  exact binary identity plus same-class compatibility checks.

- [x] 3. Make standalone Grove Shop components consume embedded config.
  **Context:** Ensure the existing non-membership Inventory path uses the same
  compiled reservation buffer as managed workers so candidate behavior is not
  accidentally served by development defaults.

- [x] 4. Prove explicit N and N+1 invocation end to end.
  **Context:** Build current/candidate configured artifacts, negotiate bootstrap
  compatibility, run current placement plus candidate-specific Orders and
  Inventory processes on one mesh, verify both ready, invoke N through current
  placement and N+1 through its explicit subject, and prove N remains the
  authoritative placement throughout.

- [x] 5. Verify and close Task 026.
  **Context:** Run formatting, diff checks, focused repetitions, vet, full
  uncached tests, and full race tests; mark DONE only after all prior E2Es pass,
  then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 025 passed all focused/full non-race and race gates and was
  pushed to `main` at `1eb5c14`.
- 2026-09-12: Kept candidate launch orchestration and authoritative placement
  cutover out of Task 026. The harness starts prebuilt local artifacts solely to
  prove coexistence; Task 027 owns health-gated control-plane handoff.
- 2026-09-12: Bootstrap negotiation passed ten repetitions; the real current
  plus candidate process topology passed three repetitions and the race
  detector. Current placement remained unchanged while both exact configured
  artifacts were explicitly invoked through System NATS.
- 2026-09-12: Hardened the existing CLI lifecycle E2E to wait for placement
  recovery after Inventory restart; its focused race run passed. Final
  verification passed `go vet ./...`, `go test -count=1 ./...`, and
  `go test -race -count=1 ./...`.
