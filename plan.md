# Plan: Replicate Grove membership through JetStream/KV

## Goal

Complete Task 013 by enabling JetStream on clustered embedded System NATS
servers, registering every Grovlet's logical identity in one replicated KV
bucket, maintaining a watched local membership view, and exposing that view
through a machine-readable per-node System NATS endpoint. Prove three real
Grovlets converge on the same membership records without adding health,
placement, or a parallel consensus mechanism.

## Context

Tasks 001 through 012 are complete. Task 012 established NATS server-route
connectivity but deliberately did not equate NATS peers with Grove members.
Task 013 introduces the first authoritative Grove control state on NATS
JetStream/KV. Task 014 owns ephemeral heartbeats and health transitions, so
membership records remain durable declarations containing only node ID and
advertised endpoint.

### Key Files

- `tasks/013-membership-view.md` — JetStream/KV membership and convergence
  requirements.
- `tasks/014-heartbeats-and-node-health.md` — boundary for liveness and health.
- `internal/systemnats/systemnats.go` — embedded NATS cluster configuration
  and transport connection.
- `internal/systemnats/membership.go` — planned KV schema, watcher-backed view,
  and query endpoint.
- `cmd/grovlet/main.go` — clustered server storage, membership lifecycle, and
  per-node API startup.
- `cmd/grovlet/main_test.go` — real-process three-node convergence E2E.

### Decisions Made

- Enable JetStream only for clustered embedded System NATS servers and store
  each server's data beneath its harness-owned or configured Grovlet runtime
  directory.
- Use one file-backed `GROVE_MEMBERSHIP` KV bucket with three replicas and
  history depth one. Keys are `nodes.<node-id>`; JSON values contain exactly
  the stable node ID and advertised endpoint.
- Treat JetStream/KV as authoritative. Each Grovlet may cache a sorted observed
  view populated by `WatchAll`, but the cache is not a second source of truth.
- Start the membership query responder before KV convergence. Its JSON response
  reports readiness, the current sorted records, and a transient error when
  initialization is still retrying.
- Create/register/watch asynchronously because the first seed Grovlet must
  publish its dynamically selected NATS route URL before a three-replica bucket
  can be created. Retry is a bounded condition loop tied to process context.
- Address query endpoints by logical node ID on System NATS so tests and the
  future CLI can ask a specific Grovlet for its locally observed view.
- Keep membership records after process exit. Health/unavailability and any
  removal policy belong to Task 014 or later recovery work.

## Sub-Tasks

- [x] 1. Select and bound Task 013.
  **Context:** Read Task 013, Task 014, accepted control-plane architecture,
  current routed server/runtime, and the pinned JetStream/KV APIs.
  **Outcome:** Chose a three-replica file-backed KV bucket, watcher-derived local
  views, asynchronous bootstrap, and a per-node JSON request/reply endpoint,
  with heartbeat health explicitly excluded.

- [ ] 2. Enable clustered JetStream storage.
  **Context:** Extend clustered embedded-server configuration with an optional
  storage directory and enable JetStream when supplied. Grovlet must use a
  node-local path under its runtime directory.
  **Acceptance:** Clustered server tests can start three JetStream peers with
  distinct storage directories while standalone behavior remains unchanged.

- [ ] 3. Implement authoritative membership and watched views.
  **Context:** Define the bucket, key/value schema, registration, retrying
  create/open behavior, WatchAll observation, sorted snapshots, and clean
  cancellation in the hidden System NATS package.
  **Acceptance:** Package tests verify three-replica bucket configuration,
  node registration, watch convergence, deterministic ordering, and context
  shutdown without a parallel state store.

- [ ] 4. Expose and manage the per-Grovlet membership API.
  **Context:** Serve a JSON membership response on a node-addressed System NATS
  subject before asynchronous KV initialization; wire its goroutine lifecycle
  into Grovlet shutdown.
  **Acceptance:** Callers can query a specific node and distinguish initializing
  from converged state; shutdown cancels and joins membership work before the
  NATS transport closes.

- [ ] 5. Prove three-Grovlet membership convergence.
  **Context:** Launch one seed and two joiners as real processes, query each
  node's local membership endpoint through its own System NATS client URL, and
  condition-wait for identical views containing all three IDs and endpoints.
  **Acceptance:** The E2E has no fixed sleeps, includes every process log on
  timeout, and proves every Grovlet observes the same three records.

- [ ] 6. Verify and close Task 013.
  **Context:** Review exported docs and schema boundaries, format, vet, repeat
  focused membership/E2E tests, run race and full suites, then mark Task 013
  DONE.
  **Acceptance:** `gofmt -l` is empty; `go vet ./...`, focused repeated tests,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass without
  weakening prior coverage.

## Log

- 2026-09-08: Tasks 001 through 012 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-08: Selected Task 013 and recorded the replicated membership schema,
  async three-node bootstrap, watcher-derived views, query API, and Task 014
  health boundary.
