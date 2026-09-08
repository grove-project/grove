# Plan: Implement the invocation serialization envelope

## Goal

Complete Task 008 by putting Gob behind small Grove helpers, defining
transport-independent request/response envelopes with request correlation and
structured failure classes, and moving local invocation plus Grove Shop's
handwritten handlers onto the accepted byte boundary without introducing NATS.

## Context

Tasks 001 through 007 are complete. Task 006 intentionally used opaque local
handler values until serialization had its own increment. Task 008 now owns the
conversion to byte handlers and envelopes. Task 009 will carry these envelopes
over System NATS; sockets, NATS concepts, destinations, membership, placement,
retry, and compatibility negotiation remain outside this task.

### Key Files

- `tasks/008-invocation-serialization-envelope.md` — required envelope fields,
  failure classes, tests, and exclusions.
- `sdk/SERIALIZATION.md` and `sdk/INVOCATION.md` — normative Gob helpers,
  transport independence, typed call shape, and error requirements.
- `serialization.go` — Encode/Decode and contextual codec errors.
- `envelope.go` — request/response envelopes and structured error classes.
- `invocation.go` — request creation, local envelope dispatch, response
  correlation, error propagation, and typed decode.
- `registry.go` — serialized local Handler contract.
- `demo/groveshop/registration.go` — explicit decode/call/encode adapters.
- matching `_test.go` files — round trips, malformed input, structured errors,
  adapted registry behavior, and the existing local Grove Shop E2E.
- `tasks/009-node-transport.md` — boundary for carrying encoded envelopes over
  System NATS.

### Decisions Made

- Implement generic `Encode[T]` and `Decode[T]` with `encoding/gob` hidden
  entirely inside the root Grove SDK package.
- Return typed `CodecError` values with operation and wrapped cause so malformed
  or unsupported values retain useful context without string assertions.
- Define `RequestEnvelope` with RequestID, ServiceID, MethodID, and Payload;
  define `ResponseEnvelope` with the same RequestID plus Payload or one
  `ResponseError`.
- Classify response failures as dispatch, handler, or serialization through an
  exported `ErrorCode`. Keep a private cause on locally constructed errors so
  `errors.Is` preserves exact local failures; Gob carries only stable code and
  message across a future process boundary.
- Change Registry Handler to `func(context.Context, []byte) ([]byte, error)` and
  update all callers. This is the canonical explicit serialization boundary,
  not a transport dependency.
- Generate process-local monotonic request IDs for Call. Task 008 requires
  correlation, not distributed identity or persistence.
- Make Client's local route accept/return envelopes. Call checks response
  correlation and decodes the application-owned response; the public generic
  call site remains unchanged for future remote routing.
- Rewrite each Grove Shop adapter as visible Decode -> concrete method -> Encode
  glue, with no raw Gob usage, reflection-driven dispatch, or generated code.

## Sub-Tasks

- [x] 1. Select and bound Task 008.
  **Context:** Read the task, serialization/invocation contracts, current Call,
  Registry, Grove Shop adapters, and Task 009 boundary.
  **Outcome:** Chose canonical byte handlers plus transport-neutral envelopes
  and preserved local causes alongside wire-safe structured errors.

- [x] 2. Implement Gob helpers and envelope types.
  **Context:** Add generic encoding/decoding, typed codec failures, request and
  response structures, error codes, local cause retention, and correlation
  mismatch handling.
  **Outcome:** Added generic Gob-backed Encode/Decode, typed operation-aware
  codec failures, correlated request/response envelopes, three stable error
  classes, and locally unwrap-capable structured response errors.

- [x] 3. Route local invocation through envelopes.
  **Context:** Convert Handler and adapters to bytes; have Call encode its
  request, create a request ID, dispatch locally, validate the response ID,
  surface structured errors, and decode the typed response.
  **Outcome:** Migrated Handler to bytes and Call to encode requests, allocate
  monotonic IDs, dispatch an envelope locally, validate response correlation,
  surface structured errors, and decode the typed result. All Grove Shop
  adapters now show the canonical Decode -> concrete call -> Encode sequence.

- [x] 4. Prove serialization and regression behavior.
  **Context:** Test payload and envelope round trips, malformed and incompatible
  input, request ID preservation, each response error class, local cause
  preservation, explicit Grove Shop adapters, and the existing local E2E.
  **Outcome:** Added value and envelope round trips, request ID preservation,
  unsupported encode, nil target, malformed/incompatible decode, dispatch,
  handler and serialization classification, local cause preservation, explicit
  adapter checks, and retained the one-Grovlet local flow.

- [x] 5. Verify and close Task 008.
  **Context:** Review `go doc`, format, vet, run repeated and race tests, run the
  full repository suite, then mark Task 008 DONE after every check succeeds.
  **Outcome:** Reviewed the complete `go doc` surfaces; `gofmt -l` reports no
  files; `go vet ./...`, 50 repeated codec/envelope/call runs, 20 repeated
  real-process local E2Es, 25 repeated Grove Shop adapter runs, `go test -race
  -count=1 ./...`, and `go test -count=1 ./...` pass. Task 008 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 007 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 008 and recorded the canonical Handler migration,
  envelope correlation, structured/local error model, and NATS boundary.
- 2026-09-08: Completed Task 008 after focused, repeated, race, vet, and full
  suite verification; no scope deviations or architectural issues found.
