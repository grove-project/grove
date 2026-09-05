# Task 024 — Embedded customer configuration

Status: TODO
Depends on: 023

## Goal
Embed customer configuration into the Grove Shop application artifact and make it observable by the application.

## Required reading
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`

## Scope
Implement:
- YAML input,
- chosen internal representation,
- compression,
- reserved binary/artifact region,
- extraction,
- clear overflow failure,
- stable config identity/revision metadata suitable for status display,
- application access to the embedded config at runtime.

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

The invalid value is intentionally preserved in the artifact so later upgrade tasks can prove candidate rejection and rollback. Do not silently sanitize it into a valid value.

Artifact identity must distinguish artifacts with different embedded configuration even when the application code version is unchanged.

## Out of scope
Live config mutation, distributed config service, automatic rebuild on overflow, candidate rollback logic, and hot config repair.

## Tests
- encode/decode round trip,
- compression/extraction,
- size/overflow bounds,
- stable config identity,
- good config is visible to Grove Shop and Inventory starts successfully,
- broken config causes deterministic Inventory startup failure,
- E2E builds artifact, embeds config, extracts it, deploys it where appropriate, and verifies the application observes it.

## Done
`go test ./...` passes.