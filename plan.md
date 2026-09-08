# Plan: Implement embedded System NATS transport

## Goal

Complete Task 009 by embedding a self-contained System NATS server, hiding NATS
connections/subjects behind an internal Grove transport, enabling Grovlets to
connect and serve transport endpoints, and proving a serialized invocation
envelope crosses between two real Grovlet processes through NATS request/reply.

## Context

Tasks 001 through 008 are complete. Task 009 introduces messaging only. Task
010 will route Grove Shop handlers remotely, Task 011 owns node identity and
advertised endpoints, Task 012 owns multi-server NATS cluster bootstrap, and
Task 013 introduces JetStream/KV. This increment needs one embedded standalone
System NATS server, explicit transport subjects, and request/reply; it must not
add discovery, placement, membership, persistence, retries, or application
access to NATS.

### Key Files

- `tasks/009-node-transport.md` — required embedded server, Grovlet connection,
  request/reply, E2E, and exclusions.
- `sdk/INVOCATION.md` and `sdk/SERIALIZATION.md` — envelope and hidden transport
  contracts.
- `docs/adr/003-embedded-nats-control-plane.md` and
  `docs/adr/004-system-data-nats-separation.md` — accepted NATS architecture.
- `internal/systemnats/systemnats.go` — embedded server lifecycle and hidden
  request/reply transport.
- `internal/systemnats/systemnats_test.go` — in-process integration coverage.
- `cmd/grovlet/main.go` — optional embedded server/connect/endpoint startup,
  readiness ordering, and shutdown.
- `cmd/grovlet/main_test.go` — configuration and real two-process envelope E2E.
- `grovetest/grovetest.go` — reusable extra command arguments for process
  topology tests.
- `tasks/010-cross-node-service-invocation.md` through
  `tasks/012-static-cluster-join.md` — remote dispatch, identity, and clustered
  NATS boundaries.

### Decisions Made

- Use the official `nats-server/v2/server` embedding API and `nats.go`
  request/reply client. Pin current stable versions in `go.mod`.
- Keep all NATS imports under `internal/systemnats` and `cmd/grovlet`; the
  application-facing Grove SDK and Grove Shop never see NATS subjects or
  connections.
- Start an embedded server on an explicit loopback listen address when a
  Grovlet receives `--system-nats-listen`. A host Grovlet connects to its own
  server; peers use `--system-nats-url`.
- Gate Grovlet readiness on server readiness, client connection, endpoint
  subscription, and NATS flush. No sleeps or asynchronous readiness guesses.
- Use explicit `_GROVE.system.invoke.*` subjects for this pre-identity stage.
  They represent System-plane transport endpoints, not membership or placement.
- Implement a transport callback over RequestEnvelope/ResponseEnvelope. The
  Task 009 Grovlet endpoint echoes the serialized payload solely as a transport
  probe; Task 010 will connect the callback to remote Registry dispatch.
- Extend `grovetest.StartNode` with variadic command arguments while rejecting
  attempts to override its owned `--runtime-dir`; restarts reuse the same args.
- E2E topology: node A embeds System NATS, node B connects to A, both expose
  explicit endpoints, and a hidden transport client requests node B through
  node A. This exercises two real processes and real loopback TCP transport.

## Sub-Tasks

- [x] 1. Select and bound Task 009.
  **Context:** Read the task, SDK contracts, accepted NATS ADRs, Tasks 010-012,
  current Grovlet/harness APIs, and official current NATS Go documentation.
  **Outcome:** Chose one embedded standalone server, hidden request/reply
  transport, explicit subjects, and deferred clustering/identity/dispatch.

- [x] 2. Add the hidden System NATS runtime.
  **Context:** Pin dependencies; implement context-bounded embedded server
  startup/shutdown, connection, subscription/flush, envelope request/reply,
  correlation validation, and typed diagnostics.
  **Outcome:** Added pinned official NATS server/client dependencies and an
  internal package with context-bounded embedded server readiness, connection,
  flushed subscriptions, correlated envelope request/reply, typed failures,
  and idempotent resource shutdown.

- [x] 3. Connect Grovlets and extend process configuration.
  **Context:** Add optional listen, URL, and endpoint flags; start/connect/serve
  before ready; shut down cleanly; let grovetest pass safe additional args.
  **Outcome:** Added optional System NATS listen/URL/subject flags, configuration
  validation, server/client/endpoint startup before ready, actual random-port
  URL publication in the ready event, and shutdown before stopped. StartNode
  now preserves safe extra args across restarts and protects its runtime flag.

- [x] 4. Prove integration and real-process transport.
  **Context:** Integration-test embedded request/reply and failures, then start
  two Grovlets, wait for both, exchange an invocation envelope through the
  embedded server into the peer process, verify payload/request ID, and clean
  up all processes and sockets.
  **Outcome:** Added an internal request/reply integration workflow and a real
  two-Grovlet E2E. The host embeds NATS on an OS-selected port, the peer connects
  from its own process, both activate endpoints before readiness, and correlated
  encoded payloads traverse the host and peer subjects without sleeps.

- [x] 5. Verify and close Task 009.
  **Context:** Review `go doc`, dependency changes, format, vet, run repeated and
  race tests, run the full repository suite, then mark Task 009 DONE after every
  check succeeds.
  **Outcome:** Reviewed public/internal `go doc` and dependency changes;
  `gofmt -l` reports no files; `go vet ./...`, 50 repeated transport
  integrations, 20 repeated two-process E2Es, `go test -race -count=1 ./...`,
  and `go test -count=1 ./...` pass. Task 009 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 008 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 009 and recorded the embedded-server topology,
  hidden System-plane API, readiness gates, explicit pre-identity subjects, and
  later remote-dispatch/bootstrap boundaries.
- 2026-09-08: Replaced free-port probing with OS-selected binding and URL data
  in the existing ready event, eliminating the only close-then-bind race.
- 2026-09-08: Completed Task 009 after focused, repeated, race, vet, and full
  suite verification; no scope deviations or architectural issues found.
