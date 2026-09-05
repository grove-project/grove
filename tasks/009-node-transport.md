# Task 009 — Embedded System NATS transport

Status: TODO
Depends on: 004, 008

## Goal
Provide real inter-process request/response transport between two Grovlets using Grove's embedded System NATS plane, without leaking NATS into application-facing SDK code.

## Required reading
- `sdk/INVOCATION.md`
- `sdk/SERIALIZATION.md`

## Scope
- Embed/start the NATS server needed by the MVP test topology.
- Connect Grovlets to System NATS.
- Use NATS request/reply or equivalent NATS messaging for transport-level request/response.
- Carry Grove's transport-independent invocation envelopes over NATS.
- Keep the transport production-shaped so the same protocol can work across physical hosts later.
- Keep System NATS concerns separated from future application-facing Data NATS concerns.

## Architectural constraints
- Application business code must not publish directly to NATS for service invocation.
- NATS subjects, connections, and request/reply details stay behind Grove's transport/invocation layer.
- Do not introduce a separate transport framework when NATS already provides the needed primitive.
- This task introduces messaging only; it does not yet introduce JetStream/KV control-state semantics.

## Out of scope
Discovery, retries, load balancing, placement, JetStream/KV membership state, generated clients, and any separate Grove-owned Raft implementation.

## Tests
Integration-test NATS-backed endpoints. E2E starts 2 real Grovlets and exchanges a Grove invocation envelope through System NATS.

## Done
Transport remains hidden behind the SDK/runtime boundary and `go test ./...` passes.