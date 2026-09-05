# Task 008 — Invocation serialization envelope

Status: TODO
Depends on: 007

## Goal
Define Grove's transport-independent request/response envelope and MVP Gob serialization.

## Required reading
- `sdk/SERIALIZATION.md`
- `sdk/INVOCATION.md`

## Scope
- Use Go `encoding/gob` behind small Grove encode/decode helpers.
- Request envelope carries at least request ID, service ID, method ID, and payload.
- Response envelope carries request ID plus payload or structured Grove error.
- Preserve enough error information to distinguish dispatch/handler/serialization failures.
- Keep the envelope independent of NATS.

## Architectural constraints
- Do not expose raw Gob encoder/decoder usage throughout business services.
- Do not introduce protobuf, generated schemas, or cross-version negotiation.
- Keep application request/response types application-owned normal Go structs.

## Out of scope
Sockets, NATS, membership, routing, retries, schema evolution machinery.

## Tests
- request/response round trips
- malformed payload
- request ID preservation
- handler error representation
- decode failure with useful context

## Done
Serialization matches `sdk/SERIALIZATION.md` and `go test ./...` passes.