# Task 009 — Embedded System NATS transport

Status: TODO
Depends on: 004, 008

## Goal
Provide real inter-process request/response transport between two Grovlets using Grove's embedded System NATS plane.

## Scope
- Embed/start the NATS server needed by the MVP test topology.
- Connect Grovlets to System NATS.
- Use NATS request/reply or equivalent NATS messaging for transport-level request/response.
- Keep the transport production-shaped so the same protocol can work across physical hosts later.
- Keep System NATS concerns separated from future application-facing Data NATS concerns.

## Architectural constraint
Do not introduce a separate transport framework for remote Grovlet control communication when NATS already provides the required primitive.

This task introduces messaging only. It does not yet introduce JetStream/KV control-state semantics.

## Out of scope
Discovery, retries, load balancing, placement, JetStream/KV membership state, and any separate Grove-owned Raft implementation.

## Tests
Integration-test NATS-backed endpoints. E2E starts 2 real Grovlets and exchanges a transport-level request through System NATS.

## Done
`go test ./...` passes.