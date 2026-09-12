# Plan: Embed immutable Grove Shop configuration

## Goal

Complete Task 024 by having the `grove` CLI delegate YAML compilation to the
target Grovlet binary, embed the returned validated representation into the
artifact's fixed region, inspect/extract it read-only, and run that same
representation in Grove Shop. Prove defaults, validation, integrity, immutable
node facts, and distinct code/config/artifact identities without introducing
rollout control state.

## Context

Tasks 001 through 023 are complete on `origin/main`. Task 023 established a
self-describing Grovlet artifact with a uniquely framed blank 4 KiB config
reservation and offline manifest/artifact inspection. Task 024 fills that
reservation. Task 025 retains ownership of publishing configured artifact and
rollout identities into authoritative JetStream/KV state.

### Key Files

- `tasks/024-embedded-customer-configuration.md` — current acceptance contract.
- `demo/groveshop/config.go` — application-owned YAML schema, defaults,
  validation, canonical output, and Gob runtime representation.
- `internal/artifact/config.go` — schema-agnostic compiler protocol,
  deterministic compression, fixed-region encoding/decoding, integrity,
  normalized code digest, embedding, and extraction.
- `cmd/grovlet/config.go` — private target-binary `config-compile` entrypoint
  and defensive runtime loading of the same compiled representation.
- `cmd/grove/main.go` — `config validate|embed|inspect|extract` CLI grammar and
  execution; it transports bytes and metadata but never parses Grove Shop YAML.
- `cmd/grovlet/*_test.go`, `cmd/grove/main_test.go`, and
  `internal/artifact/*_test.go` — compiler, runtime, CLI, corruption, identity,
  version-skew, and real configured-artifact coverage.

### Decisions Made

- Use an explicitly versioned JSON subprocess protocol for target compilation.
  The CLI sends source bytes on stdin to `<binary> config-compile`; only that
  binary applies schema, defaults, and validation and returns Gob bytes,
  canonical YAML, revision, and structured validation errors.
- Store a deterministic gzip-compressed, versioned config bundle in the fixed
  reservation. The bundle contains the target-produced runtime bytes and
  canonical YAML. The artifact layer verifies lengths and SHA-256 integrity but
  remains unaware of application fields.
- Define code identity by hashing the complete artifact with the reserved
  config bytes normalized to zero. On macOS, exclude the ad-hoc signature and
  its mutable Mach-O size metadata as platform packaging rather than code.
  Config identity hashes compiled runtime bytes; artifact identity hashes exact
  final bytes. Config-only variants must therefore share code identity and
  differ in config/artifact identity.
- Reject embedding into an already configured artifact and reject existing
  output paths. A changed configuration is produced from the canonical blank
  binary as a new immutable output artifact; no in-place or live mutation path
  is added.
- Grove Shop config includes revision, customer name, cluster name, node zone,
  and Inventory reservation buffer. Defaults are applied by the target binary;
  negative buffers fail compilation. Runtime Inventory and the read-only Web
  config endpoint consume the compiled values.
- Use `go.yaml.in/yaml/v3` with strict known-field decoding for the
  application-owned YAML boundary. No YAML dependency or schema knowledge is
  introduced into `cmd/grove` or `internal/artifact`.

## Sub-Tasks

- [x] 1. Compile Grove Shop configuration in the target binary.
  **Context:** Add typed config/defaults/strict validation/canonical YAML and a
  private compiler command with versioned structured responses. Decode the Gob
  result defensively at runtime and expose immutable identity/facts.

- [x] 2. Encode immutable configured artifacts.
  **Context:** Extend artifact inspection with code/config/artifact digests,
  deterministic compressed bundles, fixed-region bounds and integrity checks,
  new-output embedding, and extraction. Cover round trips, overflow,
  configured-input rejection, and corruption/incompatibility.

- [x] 3. Add schema-agnostic config CLI commands.
  **Context:** Implement `config validate`, `embed`, `inspect`, and `extract`.
  Delegate validation to the selected target executable, preserve structured
  errors, and test with fake compiler semantics to prove CLI/target version
  separation.

- [x] 4. Apply compiled configuration in Grove Shop.
  **Context:** Construct Inventory from the embedded reservation buffer, expose
  revision/customer/cluster/zone/config digest through a read-only Web endpoint
  and Grovlet readiness, and retain safe defaults for the blank development
  artifact.

- [x] 5. Prove configured variants end to end.
  **Context:** Build the base artifact, use the real CLI and target compiler to
  create cloud and edge variants, inspect/extract them, assert common code but
  distinct config/artifact digests, reject invalid YAML, deploy a configured
  artifact, and verify exactly compiled runtime behavior through real processes.

- [x] 6. Verify and close Task 024.
  **Context:** Run formatting, diff checks, focused repetitions, vet, the full
  uncached suite, and the full race suite before marking the task DONE.

## Log

- 2026-09-12: Task 023 passed all focused/full non-race and race tests and was
  pushed to `main` at `2344ba4`.
- 2026-09-12: Kept configuration bytes out of System NATS/KV; Task 024 identity
  remains local artifact/runtime metadata until Task 025 introduces replicated
  rollout records.
- 2026-09-12: Focused Task 024 tests passed three consecutive runs and under
  the race detector, including real cloud/edge artifact compilation,
  inspection, extraction, deployment, and configured application behavior.
- 2026-09-12: Configured Mach-O artifacts are ad-hoc re-signed after embedding
  so macOS can execute them. Signature blobs and signer-mutated load metadata
  are normalized out of code identity; exact signed bytes remain represented
  by artifact identity.
- 2026-09-12: Task 024 passed `go vet ./...`, `go test -count=1 ./...`,
  and `go test -race -count=1 ./...`; marked the task DONE with no Task 025
  rollout or replicated-state behavior introduced.
