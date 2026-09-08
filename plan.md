# Plan: Track Grovlet health with System NATS heartbeats

## Goal

Complete Task 014 by exchanging ephemeral heartbeats over the shared System
NATS fabric, tracking receiver-side last-seen times, deriving
healthy/unavailable transitions for durable membership records, and exposing a
machine-readable per-Grovlet cluster view. Prove both survivors report a killed
node unavailable without implementing relocation or another state store.

## Context

Tasks 001 through 013 are complete. Task 013 made JetStream/KV membership the
authoritative logical node set and gave each Grovlet a watched local view.
Task 014 adds transient liveness observations only. Task 015 owns placement and
later tasks own recovery, so health changes must not trigger service movement.

### Key Files

- `tasks/014-heartbeats-and-node-health.md` — heartbeat, last-seen, transition,
  and failure E2E requirements.
- `tasks/015-explicit-service-placement.md` — boundary for placement state.
- `internal/systemnats/membership.go` — authoritative membership observations
  consumed by health evaluation.
- `internal/systemnats/health.go` — planned ephemeral heartbeat tracker and
  machine-readable cluster view.
- `cmd/grovlet/main.go` — health lifecycle for membership-enabled Grovlets.
- `cmd/grovlet/main_test.go` — real-process kill and survivor-convergence E2E.

### Decisions Made

- Publish JSON heartbeats on node-addressed `_GROVE.system.heartbeat.*`
  subjects and subscribe through the existing System NATS connection.
- Record receiver time as last-seen so health does not depend on synchronized
  node clocks. Heartbeat payload timestamps remain diagnostic only.
- Derive health only for nodes in the watched authoritative membership view.
  Membership records remain after a process dies; they transition locally from
  healthy to unavailable when their heartbeat deadline expires.
- Keep heartbeat observations and derived health in a synchronized in-memory
  cache. They are ephemeral observations, not a competing authoritative store,
  and no heartbeat writes to JetStream/KV.
- Use a 100 ms default heartbeat interval and a 500 ms unavailable threshold
  for the MVP. Production tuning and advanced quorum/split-brain policy remain
  out of scope.
- Expose a separate per-node JSON cluster-view subject containing sorted node
  identity, endpoint, health, and last-seen fields. Preserve Task 013's raw
  membership endpoint.
- Start the cluster-view responder before the health loop, publish an immediate
  first heartbeat after subscription activation, and cancel/join health work
  before membership and transport shutdown.

## Sub-Tasks

- [x] 1. Select and bound Task 014.
  **Context:** Read Task 014, Task 015, failure-detection ADR, current
  membership/runtime APIs, and System NATS transport lifecycle.
  **Outcome:** Chose receiver-time ephemeral heartbeats, membership-scoped
  derived health, a separate cluster-view endpoint, and no KV heartbeat writes,
  placement, recovery, or parallel consensus.

- [ ] 2. Implement heartbeat tracking and cluster views.
  **Context:** Define heartbeat payloads, healthy/unavailable states, last-seen
  tracking, periodic publish/evaluation, deterministic membership-scoped
  snapshots, and context-driven shutdown.
  **Acceptance:** Package tests prove initial healthy convergence, live
  last-seen updates, timeout transition, sorted output, and clean cancellation.

- [ ] 3. Expose the machine-readable cluster-view API.
  **Context:** Add per-node JSON System NATS serve/request operations that
  report initializing, healthy, and unavailable states without changing the
  membership KV schema or endpoint.
  **Acceptance:** API tests query a specific observer and decode the complete
  deterministic cluster view.

- [ ] 4. Wire health into membership-enabled Grovlets.
  **Context:** Construct health from the existing membership observer, serve its
  endpoint before launch, and order cancellation/join ahead of membership,
  transport, and embedded-server shutdown.
  **Acceptance:** Existing membership and route-only E2Es remain green and
  graceful shutdown leaves no health goroutine behind.

- [ ] 5. Prove killed-node health convergence.
  **Context:** Start three real mutually seeded Grovlets, condition-wait until
  all report healthy, kill the middle process, then query both survivors until
  each reports that membership record unavailable while reporting themselves
  healthy.
  **Acceptance:** The E2E uses no fixed sleeps and dumps all process logs on any
  bounded-wait failure.

- [ ] 6. Verify and close Task 014.
  **Context:** Review exported docs and state boundaries, format, vet, repeat
  focused health/E2E tests, run race and full suites, then mark Task 014 DONE.
  **Acceptance:** `gofmt -l` is empty; `go vet ./...`, focused repeated tests,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass without
  weakening prior coverage.

## Log

- 2026-09-08: Tasks 001 through 013 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-08: Selected Task 014 and recorded ephemeral receiver-time
  heartbeats, membership-scoped derived health, cluster-view API, and the
  placement/recovery boundary.
