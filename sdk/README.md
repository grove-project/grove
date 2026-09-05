# Grove SDK Contract

## Purpose
This folder defines the developer-facing Grove SDK contract for the MVP. These files are normative implementation guidance for coding agents and contributors.

Grove's runtime may evolve internally, but the MVP SDK should remain explicit, small, unsurprising, and natural for Go developers.

## Core principles
- Business services remain ordinary Go types with ordinary methods.
- No required service interfaces.
- No generated RPC stubs or build-time code generation.
- No reflection-driven magic registration.
- Developers explicitly register each remotely invokable method.
- Service IDs and method IDs are explicit stable constants.
- Distribution is visible at the call boundary; Grove does not pretend a remote call is an ordinary in-process function call.
- Local and remote Grove invocation use the same Grove call API.
- Business packages remain directly unit-testable without starting Grove.
- The SDK should preserve normal IDE code navigation to concrete implementations.
- The MVP serialization format is Go `encoding/gob` behind explicit Grove encode/decode helpers.

## Normative documents
- `DESIGN_PRINCIPLES.md` — constraints and non-goals.
- `SERVICE_MODEL.md` — service IDs, method IDs, registration, and dispatch.
- `INVOCATION.md` — explicit application-facing call model and local/remote behavior.
- `SERIALIZATION.md` — request/response envelope and Gob encoding.
- `EXAMPLE.md` — canonical Grove Shop example.

## Agent rule
When a numbered task conflicts with these SDK documents, do not silently invent a different SDK. Report the conflict and implement the smallest solution consistent with this contract unless the task explicitly supersedes it.