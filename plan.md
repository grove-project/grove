# Plan: Implement cross-node service invocation

## Goal

Complete Task 010 by routing the existing generic `grove.Call` API through
System NATS when an explicit destination is remote, dispatching into the peer's
Registry, and proving Grove Shop Orders runs in one real Grovlet while Inventory
runs in another with application code unchanged at the call site.

## Context

Tasks 001 through 009 are complete. Task 010 owns explicit-destination remote
dispatch only. Task 011 introduces stable node identity and advertised
endpoints, while Tasks 12-15 introduce shared NATS bootstrap, membership, and
placement. This task must not infer destinations, retry, fail over, load
balance, or add a second remote call API.

### Key Files

- `tasks/010-cross-node-service-invocation.md` and the SDK invocation/service/
  serialization contracts — required same-API remote semantics.
- `invocation.go` — route abstraction, local Dispatcher, routed Client, and
  public transport failure classification.
- `internal/systemnats/systemnats.go` — hidden subject route implementing the
  Grove route contract over NATS request/reply.
- `cmd/grovlet/main.go` — explicit reference-app placement flags and Registry
  composition for Orders or Inventory endpoints.
- `cmd/grovlet/main_test.go` — two-process Orders-to-Inventory E2E.
- `demo/groveshop/groveshop.go` — unchanged explicit Inventory call site.
- `tasks/011-node-identity-and-endpoint.md` — boundary for replacing explicit
  transport subjects with stable node metadata.

### Decisions Made

- Add a small runtime `Router` contract used internally by Client. `Call`
  remains unchanged; `NewClient` constructs the local route and
  `NewRoutedClient` accepts runtime routing supplied outside business code.
- Extract local envelope execution into `Dispatcher`, which resolves the
  Registry and returns the same ResponseEnvelope used remotely.
- Let `internal/systemnats` create a routed Grove Client for an explicit
  subject. NATS types, subjects, and connections remain absent from Grove Shop.
- Add a public transport sentinel wrapped by hidden transport errors so
  applications can distinguish transport failure without importing NATS.
- Configure the reference binary explicitly for this incremental E2E:
  Inventory registration on the peer, or Orders registration on the host with
  an explicit Inventory subject. These are application composition flags, not
  discovery, identity, membership, or placement state.
- Keep Task 009's no-service endpoint as a transport echo probe. When Grove
  Shop placement flags are present, the endpoint dispatches its Registry.
- E2E starts A with embedded NATS and Orders targeting B's subject, starts B
  with Inventory, creates a routed Client to A, calls Orders through the normal
  generic API, and asserts the complete cross-process order result and remote
  error class.

## Sub-Tasks

- [x] 1. Select and bound Task 010.
  **Context:** Read the task and normative contracts, current Client/envelopes,
  hidden NATS transport, Grove Shop composition, and Task 011 boundary.
  **Outcome:** Chose one Client/Call API with local and explicit-subject routes,
  shared Registry dispatch, and no placement or discovery.

- [x] 2. Generalize Client routing and Registry dispatch.
  **Context:** Add Router, Dispatcher, routed Client construction, and public
  transport failure classification while preserving local errors and tests.
  **Outcome:** Added the runtime Router contract, routed Client construction,
  reusable local Dispatcher, transport failure sentinel/code, and propagation
  of nested remote failure classes. Existing local Client and Call behavior is
  unchanged.

- [x] 3. Compose remote Grove Shop endpoints in Grovlets.
  **Context:** Add explicit Orders/Inventory placement flags; build registries,
  create the Orders-to-peer routed client, and serve Dispatcher responses over
  the existing System NATS endpoint.
  **Outcome:** Added explicit reference-app placement flags. A Grovlet can
  register Inventory or construct Orders with an Inventory client routed to an
  explicit peer subject, then serve the same Registry Dispatcher over its
  existing System NATS endpoint.

- [x] 4. Prove same-API cross-node behavior.
  **Context:** Start two real Grovlets, invoke Orders on A with ordinary
  `grove.Call`, traverse NATS to Inventory on B, verify the completed business
  result, verify structured remote failure, and clean up without sleeps.
  **Outcome:** Added a two-real-process E2E where the test calls Orders on A,
  A executes its registered handler and calls Inventory on B, and the completed
  order returns through both request/reply hops. Invalid Inventory input returns
  a structured remote handler failure; hidden routed-client tests also classify
  unavailable subjects as transport failures.

- [x] 5. Verify and close Task 010.
  **Context:** Review `go doc`, format, vet, run repeated and race tests, run the
  full repository suite, then mark Task 010 DONE after every check succeeds.
  **Outcome:** Reviewed all affected `go doc` surfaces; `gofmt -l` reports no
  files; `go vet ./...`, 25 repeated two-process Grove Shop E2Es, 50 repeated
  routed transport integrations, 50 repeated Call tests, `go test -race
  -count=1 ./...`, and `go test -count=1 ./...` pass. Task 010 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 009 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 010 and recorded shared dispatch, hidden NATS
  routing, explicit reference-app composition, and the identity/placement
  boundary.
- 2026-09-08: Completed Task 010 after focused, repeated, race, vet, and full
  suite verification; no scope deviations or architectural issues found.
