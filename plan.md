# Plan: Define the Grove Shop deployment artifact

## Goal

Complete Task 023 by making the Grovlet executable a self-describing immutable
Grove Shop artifact containing application/runtime metadata, application code,
embedded Web UI assets, and a fixed-capacity reserved customer-configuration
region. Deploy that exact binary to three real Grovlets and prove its Web and
business paths without implementing configuration embedding or rollout.

## Context

Tasks 001 through 022 are complete on `origin/main`. The current Grovlet binary
already contains and launches Grove Shop Orders and Inventory workers. Task 023
adds the artifact boundary and the first Grove-managed Web worker. Task 024
retains ownership of target-binary configuration validation, config
embedding/extraction commands, runtime config access, and separate code/config
identity.

### Key Files

- `tasks/023-deployment-artifact-contract.md` — current acceptance contract.
- `internal/artifact/` — versioned manifest, reserved-region framing, offline
  executable inspection, validation, and digest calculation.
- `cmd/grovlet/artifact.go` — Grove Shop manifest and blank reserved region
  compiled into the shipped executable.
- `cmd/grovlet/main.go` and `cmd/grovlet/worker.go` — Web component declaration,
  placement, and managed worker lifecycle.
- `demo/groveshop/web.go` and `demo/groveshop/web/` — embedded HTTP assets and
  handler shipped inside the artifact.
- `cmd/grovlet/main_test.go` — real artifact build, inspection, cluster deploy,
  HTTP, and cross-node business E2E.

### Decisions Made

- Treat the existing Grovlet executable as the single deployable application
  artifact: every node receives the same binary and flags select which declared
  components it hosts. Do not introduce per-service binaries or packaging.
- Embed a small, explicitly versioned JSON manifest with Grove Shop identity,
  code version, component entrypoints/runtime mode, UI asset paths, and reserved
  config capacity. Compute SHA-256 metadata and complete-artifact digests during
  offline inspection; avoid a self-referential digest inside the binary.
- Compile a uniquely framed fixed-capacity blank byte region into the artifact.
  Task 023 only validates and exposes the reservation. Task 024 will define its
  config payload/header and post-build mutation workflow.
- Add Web as a normal managed component worker. It listens on an explicitly
  configured local address and serves only an embedded filesystem, so the E2E
  needs neither a separate frontend deployment nor loose runtime assets.
- Keep Payment and Shipping as the existing in-process Orders dependencies for
  this increment. Declaring or placing them independently would extend beyond
  the Task 023 Web/artifact proof.

## Sub-Tasks

- [x] 1. Define and inspect the immutable artifact envelope.
  **Context:** Add manifest/inspection types, strict validation, unique binary
  framing, a fixed blank config reservation, and SHA-256 identities. Cover
  valid, malformed, missing, and file-inspection paths with unit tests.
  **Outcome:** `internal/artifact` validates a versioned JSON manifest and its
  uniquely framed 4 KiB config reservation from executable bytes or a file.
  Inspection reports exact metadata and complete-artifact SHA-256 digests and
  detects blank versus populated reservation bytes. Unit tests cover malformed,
  missing, short, blank, populated, and file-backed inputs.

- [x] 2. Embed and serve the basic Grove Shop UI.
  **Context:** Add the basic single-page Orders/Cluster Status shell as embedded
  assets and run its HTTP handler from a Grove-managed Web worker with explicit
  placement and lifecycle metadata.
  **Outcome:** The Grove Shop package embeds a responsive Orders and Cluster
  Status shell. Service 5 declares Web, and the Grovlet starts/stops its HTTP
  server as a normal managed worker using explicit placement and listen
  configuration.

- [x] 3. Prove the built artifact end to end.
  **Context:** Build one Grovlet artifact, inspect its manifest and digests,
  verify the UI fingerprint is in the executable, deploy that same path across
  three real Grovlets, request the Web root, and complete an Orders-to-Inventory
  call through placement.
  **Outcome:** `TestGroveShopArtifactDeploys` inspects the built Grovlet,
  verifies its exact embedded UI bytes, deploys the unchanged executable to
  three processes, observes Web/Orders/Inventory placement, receives the UI
  over HTTP, and completes a placement-routed order.

- [ ] 4. Verify and close Task 023.
  **Context:** Run formatting, diff checks, focused repetitions, vet, the full
  uncached suite, and the full race suite before marking the task DONE.

## Log

- 2026-09-11: Task 022 passed all focused/full non-race and race tests and was
  pushed to `main` at `c19972d`.
- 2026-09-11: Reserved all semantic customer-config compilation, mutation,
  extraction, and runtime use for Task 024; Task 023 establishes only the fixed
  artifact reservation and inspection contract.
- 2026-09-11: The artifact deployment E2E passed three consecutive runs in
  10.63–15.33 seconds and a race-enabled run in 15.90 seconds.
