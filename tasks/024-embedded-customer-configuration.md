# Task 024 — Embedded customer and node configuration

Status: TODO
Depends on: 023

## Goal
Embed deployment configuration into the Grove application artifact, expose it read-only at runtime, and establish the invariant that configuration changes create new artifacts rather than mutating running nodes.

## Required reading
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`
- `docs/developer-experience/deployment.md`
- `docs/cli/rollouts.md`

## Scope
Implement:
- YAML input,
- typed/internal representation,
- compression,
- reserved binary/artifact region,
- extraction and read-only inspection,
- clear overflow failure,
- stable config identity/revision metadata suitable for status display,
- application/Grovlet access to embedded config at runtime,
- immutable cluster/node facts such as node zone/class as part of embedded configuration,
- separate code digest, config digest, and final artifact digest.

Example configured variants may contain identical code but different node facts:

```yaml
cluster:
  name: production
node:
  zone: cloud
```

and:

```yaml
cluster:
  name: production
node:
  zone: edge
```

Grove must read these values from the embedded configuration. It must not infer authoritative node zone/class from IP ranges or network heuristics.

Use the Grove Shop Inventory configuration scenario:

```yaml
inventory:
  reservation_buffer: 100
```

is healthy, while:

```yaml
inventory:
  reservation_buffer: -1
```

is invalid and must cause Inventory to fail deterministically during initialization/startup.

The invalid value is intentionally preserved in the artifact so later rollout tasks can prove candidate rejection and rollback. Do not silently sanitize it.

## Architectural invariants
- A running Grove artifact's configuration is immutable.
- There is no runtime config-set path in the MVP.
- Changing configuration produces another artifact, even when executable code is unchanged.
- CLI extraction/inspection is observational; it does not mutate a running process.
- Artifact identity distinguishes configured variants of identical code.

## Out of scope
Live config mutation, distributed config service, automatic rebuild on overflow, candidate handoff/rollback logic, and hot config repair.

## Tests
- encode/decode round trip,
- compression/extraction,
- size/overflow bounds,
- stable config identity,
- identical code + different config => same code digest and different config/artifact digests,
- cloud/edge configured variants expose their embedded zone correctly,
- good config is visible to Grove Shop and Inventory starts successfully,
- broken config causes deterministic Inventory startup failure,
- E2E builds artifact, embeds config, extracts/inspects it, deploys it where appropriate, and verifies the application observes it.

## Done
`go test ./...` passes.
