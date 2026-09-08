# Plan: Bootstrap a shared System NATS cluster

## Goal

Complete Task 012 by allowing explicitly identified Grovlets to start embedded
NATS route listeners, join through a configured seed route, and announce
readiness only after the route is established. Prove three real Grovlet
processes exchange control-plane messages through distinct embedded servers in
one System NATS fabric without introducing Grove membership or consensus.

## Context

Tasks 001 through 011 are complete. Task 012 owns only NATS server-cluster
bootstrap. Task 013 will enable JetStream/KV and define authoritative Grove
membership, so NATS route connectivity must not be presented as logical node
membership or persisted as Grove control state.

### Key Files

- `tasks/012-static-cluster-join.md` — explicit seed bootstrap and three-process
  E2E requirements.
- `tasks/013-membership-view.md` — boundary for JetStream/KV and logical Grove
  membership.
- `internal/systemnats/systemnats.go` — embedded server lifecycle and hidden
  NATS transport.
- `internal/systemnats/systemnats_test.go` — server-cluster and cross-route
  transport contract tests.
- `cmd/grovlet/main.go` — process flags, startup validation, and readiness
  metadata.
- `cmd/grovlet/main_test.go` — real-process bootstrap E2E and configuration
  validation.

### Decisions Made

- Keep the existing standalone `StartServer` entry point and add a clustered
  server entry point configured with a node name, client listener, route
  listener, and explicit seed route URLs.
- Use NATS server routes and NATS's own cluster behavior. Do not add a Grove
  discovery, election, membership, or consensus protocol.
- A clustered Grovlet embeds its own NATS server and connects its runtime
  transport to that local server. Joiners receive a seed's route URL, not its
  client URL.
- Allow port zero for both client and route listeners so the embedded server
  selects ports without a test-side reservation race. Publish both resulting
  URLs in the ready event.
- Require Task 011 identity when clustered startup is configured so the NATS
  server name is stable and independent of host identity.
- A joiner does not report ready until its embedded server has at least one
  established NATS route. Bounded condition waits carry startup context errors;
  no fixed sleeps are used.
- Prove one shared plane by connecting a test client to the seed's client URL
  and requesting unique endpoint subjects hosted by all three Grovlets.

## Sub-Tasks

- [x] 1. Select and bound Task 012.
  **Context:** Read Task 012, Task 013, accepted NATS ADRs, SDK transport
  boundaries, the current embedded server/runtime, and the pinned NATS API.
  **Outcome:** Chose explicit NATS route seeding with independently embedded
  servers, route-gated readiness, and no JetStream/KV or Grove membership.

- [ ] 2. Extend the embedded System NATS server for routed clustering.
  **Context:** Add configuration for node name, client/route listeners, and seed
  URLs; expose the selected route URL; and wait for at least one route when a
  join seed is configured while preserving standalone startup.
  **Acceptance:** Package tests start independently embedded servers, join them
  through a seed, and exchange a Grove envelope across the NATS route.

- [ ] 3. Configure clustered Grovlet startup and readiness.
  **Context:** Add route-listen and seed flags, reject incomplete or conflicting
  combinations, start the clustered embedded server using the explicit node ID,
  and include the route URL in the machine-readable ready event.
  **Acceptance:** Configuration tests cover valid bootstrap plus invalid seed,
  listener, external-URL, and missing-identity combinations; prior lifecycle
  output stays compatible.

- [ ] 4. Prove a three-Grovlet shared System NATS plane.
  **Context:** Start one seed and two joiners as real OS processes with unique
  runtime directories, identities, client listeners, route listeners, and
  endpoint subjects. Use each joiner's explicit seed configuration and request
  every subject through only the seed's client endpoint.
  **Acceptance:** The E2E uses bounded waits, emits process logs on timeout,
  verifies three distinct client/route URLs, and receives correlated responses
  from all three Grovlets.

- [ ] 5. Verify and close Task 012.
  **Context:** Review exported docs, format, vet, repeat focused package and E2E
  tests, run race and full suites, then mark Task 012 DONE.
  **Acceptance:** `gofmt -l` is empty; `go vet ./...`, focused repeated tests,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass without
  weakening prior coverage.

## Log

- 2026-09-08: Tasks 001 through 011 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-08: Selected Task 012 and recorded NATS route seeding,
  independently embedded servers, route-gated readiness, and the Task 013
  membership boundary.
