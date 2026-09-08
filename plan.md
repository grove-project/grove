# Plan: Implement node identity and advertised endpoints

## Goal

Complete Task 011 by giving each configured Grovlet an explicit validated node
ID and advertised transport endpoint, keeping that identity stable for the
process lifetime, publishing it in readiness, and proving same-host Grovlets
remain distinct and addressable without introducing discovery or membership.

## Context

Tasks 001 through 010 are complete. Task 011 owns process-lifetime identity
configuration and readiness only. Task 012 owns cluster bootstrap and Task 013
owns authoritative membership. Identity must therefore remain explicit input;
it is not inferred from hostname, persisted as machine identity, discovered,
or written into cluster state.

### Key Files

- `tasks/011-node-identity-and-endpoint.md` — identity, validation, readiness,
  and E2E scope.
- `cmd/grovlet/main.go` — flags, validation, runtime identity, and ready event.
- `cmd/grovlet/main_test.go` — validation and same-host addressability proof.
- `tasks/012-static-cluster-join.md` — boundary for seed-based shared fabric.

### Decisions Made

- Add paired optional `--node-id` and `--advertise-endpoint` flags. If either is
  supplied both are required; legacy lifecycle-only tests may omit both.
- Accept node IDs containing letters, digits, dot, underscore, or hyphen, with
  an alphanumeric first character. This is explicit logical identity and never
  derives from the host.
- Require advertised endpoints to be absolute URLs with a scheme and host.
  Grove does not infer or probe them in this task.
- Add NodeID and AdvertisedEndpoint to the machine-readable ready event. Values
  remain unchanged until that process exits; restart may reuse the same inputs.
- Extend the real cross-node Grove Shop E2E with distinct IDs and endpoint URLs,
  assert both readiness records, and retain its successful routing proof as
  evidence that the nodes remain addressable on one host.

## Sub-Tasks

- [x] 1. Select and bound Task 011.
  **Context:** Read Task 011, current Grovlet transport configuration/readiness,
  the invocation contract, and Task 012.
  **Outcome:** Chose explicit paired configuration and readiness metadata with
  no discovery, persistence, or membership semantics.

- [ ] 2. Implement and validate identity configuration.
  **Context:** Add flags, pairing rules, ID syntax, endpoint URL validation, and
  immutable runtime values.
  **Outcome:** Pending.

- [ ] 3. Publish identity through readiness.
  **Context:** Extend the existing NDJSON ready record without changing legacy
  output when identity is absent.
  **Outcome:** Pending.

- [ ] 4. Prove same-host distinction and addressability.
  **Context:** Configure the two cross-node E2E Grovlets with distinct IDs and
  endpoints, verify their ready records, and retain the successful remote order
  flow as the addressability assertion.
  **Outcome:** Pending.

- [ ] 5. Verify and close Task 011.
  **Context:** Review docs, format, vet, repeat identity/cross-node tests, run
  race and full suites, then mark Task 011 DONE.
  **Outcome:** Pending.

## Log

- 2026-09-08: Tasks 001 through 010 completed with all focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-08: Selected Task 011 and recorded paired explicit configuration,
  readiness publication, same-host E2E reuse, and the membership boundary.
