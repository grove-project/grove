# Task 024 — Embedded customer and node configuration

Status: TODO
Depends on: 023

## Goal
Embed deployment configuration into the Grove application artifact, expose it read-only at runtime, and establish the invariant that configuration changes create new artifacts rather than mutating running nodes.

The target binary owns configuration semantics. The Grove CLI transports and embeds configuration, but the exact binary that will consume the configuration at runtime must parse, apply defaults, validate, and compile it during the embedding workflow.

## Required reading
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`
- `docs/developer-experience/deployment.md`
- `docs/cli/rollouts.md`

## Configuration compilation contract

Conceptually:

```text
YAML
  ↓
grove config embed
  ↓
target binary config-compile entrypoint
  ├─ parse using its own config types/schema
  ├─ apply its own defaults
  ├─ validate using its own rules
  ├─ produce its runtime representation
  └─ serialize/return validated payload + metadata
  ↓
CLI compresses/embeds payload into reserved section
  ↓
immutable configured artifact
```

The CLI must not maintain a second implementation of the application's configuration schema, defaults, or validation rules. This avoids version skew such as a new CLI validating configuration differently from the older binary that will actually run it.

> **The configuration producer and configuration consumer are the same binary version.**

A CLI invocation may look like:

```bash
grove config embed --binary ./grove-v8 --config production.yaml --output ./grove-v8-production
```

Internally, the CLI invokes a reserved Grove config-compilation mode/entrypoint in `./grove-v8`. The exact private command/IPC mechanism is an implementation detail, but it must be version-local to the target binary.

The binary returns either a validated compiled representation plus metadata, or a structured validation error. The CLI writes only a successfully compiled representation into the final artifact.

At runtime that same binary version decodes/uses the representation it produced during embedding.

## Scope
Implement:
- YAML input at the CLI boundary,
- a target-binary config compilation/validation entrypoint,
- application-owned typed configuration/schema/defaults/validation,
- compiled runtime representation,
- compression,
- reserved binary/artifact region,
- extraction and read-only inspection,
- clear validation and overflow failures,
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

## Validation behavior

Invalid configuration is rejected during embedding by the target binary before a configured artifact is produced.

For example:

```yaml
inventory:
  reservation_buffer: 100
```

is accepted, while:

```yaml
inventory:
  reservation_buffer: -1
```

is rejected by the Grove Shop binary's own validation rules during config compilation.

Runtime should still defensively verify the embedded representation/integrity and fail safely if an artifact is corrupt or incompatible. Normal semantic configuration errors, however, should be caught before embedding because the same binary owns both compilation and runtime consumption.

Rollout-failure demos that intentionally need a candidate to start and become unhealthy should use a configuration value that is semantically valid at embed time but causes a deterministic runtime/environmental health failure; they should not rely on bypassing config validation.

## CLI boundary

The CLI knows how to:
- invoke the target binary's config compiler,
- receive compiled bytes and metadata,
- compress/embed them,
- enforce reserved-section capacity,
- extract bytes/config for inspection,
- display structured validation errors.

The CLI does **not** own application configuration semantics.

Related commands may include:

```bash
grove config validate --binary ./grove-v8 --config production.yaml
grove config embed --binary ./grove-v8 --config production.yaml --output ./grove-v8-production
grove config inspect --binary ./grove-v8-production
grove config extract --binary ./grove-v8-production --output production.yaml
```

`validate` and `embed` delegate semantic understanding to the target binary.

## Architectural invariants
- A running Grove artifact's configuration is immutable.
- There is no runtime config-set path in the MVP.
- Changing configuration produces another artifact, even when executable code is unchanged.
- The target binary defines, defaults, validates, and compiles its own configuration.
- Grove CLI must not duplicate application configuration validation/schema logic.
- The configuration producer and runtime consumer are the same binary version.
- CLI extraction/inspection is observational; it does not mutate a running process.
- Artifact identity distinguishes configured variants of identical code.

## Out of scope
Live config mutation, distributed config service, CLI-owned application schemas, automatic rebuild on overflow, candidate handoff/rollback logic, and hot config repair.

## Tests
- target binary accepts valid YAML and produces compiled config,
- target binary rejects invalid YAML/config with structured diagnostics,
- defaults applied during compilation equal defaults observed at runtime,
- encode/decode round trip between target-binary compiler and target-binary runtime,
- compression/extraction,
- size/overflow bounds,
- stable config identity,
- identical code + different config => same code digest and different config/artifact digests,
- cloud/edge configured variants expose their embedded zone correctly,
- CLI version skew does not change target-binary validation semantics,
- corrupted/incompatible embedded payload fails safely at runtime,
- E2E builds artifact, asks that binary to validate/compile config, embeds it, extracts/inspects it, deploys it, and verifies the application observes exactly the compiled configuration.

## Done
`go test ./...` passes.
