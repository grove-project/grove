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

- Enable JetStream only for membership-enabled clustered System NATS servers
  and store each server's data beneath its harness-owned or configured Grovlet
  runtime directory.
- Use one file-backed `GROVE_MEMBERSHIP` KV bucket with three replicas and
  history depth one. Keys are `nodes.<node-id>`; JSON values contain exactly
  the stable node ID and advertised endpoint.
- Treat JetStream/KV as authoritative. Each Grovlet may cache a sorted observed
  view populated by `WatchAll`, but the cache is not a second source of truth.
- Start the membership query responder before KV convergence. Its JSON response
  reports readiness, the current sorted records, and a transient error when
  initialization is still retrying.
- Require every membership-enabled NATS peer to have an explicit route seed,
  as clustered JetStream itself requires configured routes. Tests preselect
  isolated route ports, start mutually seeded processes before waiting for
  readiness, and keep Task 012's dynamic non-JetStream bootstrap intact.
- Create/register/watch asynchronously while the JetStream meta group and
  three-replica bucket converge. Retry is a bounded condition loop tied to
  process context.
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

- [x] 2. Enable clustered JetStream storage.
  **Context:** Extend clustered embedded-server configuration with an optional
  storage directory and enable JetStream when supplied. Grovlet must use a
  node-local path under its runtime directory.
  **Outcome:** Clustered server configuration now optionally enables JetStream
  with a caller-owned storage directory. Membership-enabled Grovlets use a
  node-local path below their runtime directory, while standalone and Task 012
  route-only startup remain unchanged.

- [x] 3. Implement authoritative membership and watched views.
  **Context:** Define the bucket, key/value schema, registration, retrying
  create/open behavior, WatchAll observation, sorted snapshots, and clean
  cancellation in the hidden System NATS package.
  **Outcome:** Added the file-backed three-replica `GROVE_MEMBERSHIP` bucket,
  `nodes.<node-id>` JSON records, retrying registration, WatchAll observation,
  sorted snapshots, transient readiness/error state, and clean context
  cancellation. Tests add members one by one and prove existing watchers update.

- [x] 4. Expose and manage the per-Grovlet membership API.
  **Context:** Serve a JSON membership response on a node-addressed System NATS
  subject before asynchronous KV initialization; wire its goroutine lifecycle
  into Grovlet shutdown.
  **Outcome:** Added per-node System NATS JSON query subjects and request
  helpers. Grovlet serves the initializing view before launching membership
  work, then cancels and joins that work before closing transport and server.

- [x] 5. Prove three-Grovlet membership convergence.
  **Context:** Launch one seed and two joiners as real processes, query each
  node's local membership endpoint through its own System NATS client URL, and
  condition-wait for identical views containing all three IDs and endpoints.
  **Outcome:** Added a real three-process E2E with mutually seeded static route
  listeners. It queries each Grovlet through its local NATS client URL and
  condition-waits for the same sorted three-record view, dumping every process
  log on timeout.

- [x] 6. Verify and close Task 013.
  **Context:** Review exported docs and schema boundaries, format, vet, repeat
  focused membership/E2E tests, run race and full suites, then mark Task 013
  DONE.
  **Outcome:** Reviewed the exported schema/API and complete diff;
  `gofmt -l .` and `git diff --check` are clean. `go vet ./...`, 10 repeated
  package membership/cluster tests, 10 repeated real-process
  membership/route-only E2Es, `go test -race -count=1 ./...`, and
  `go test -count=1 ./...` pass. Task 013 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 012 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-08: Selected Task 013 and recorded the replicated membership schema,
  async three-node bootstrap, watcher-derived views, query API, and Task 014
  health boundary.
- 2026-09-08: Enabled per-node JetStream storage and added the authoritative KV
  schema, retrying registration, watcher-derived sorted views, and JSON query
  API.
- 2026-09-08: Wired membership lifecycle into Grovlet and passed focused
  package tests plus the three-process convergence E2E.
- 2026-09-08: Completed Task 013 after repeated, vet, race, formatting, and full
  suite verification; no scope deviations or architectural issues found.
