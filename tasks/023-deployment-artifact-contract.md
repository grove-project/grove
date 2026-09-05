# Task 023 — Deployment artifact contract

Status: TODO
Depends on: 019

## Goal
Define the MVP Grove application artifact contract used by Grove Shop.

## Required reading
- `demo/ARCHITECTURE.md`
- `demo/UI.md`
- `demo/IMPLEMENTATION_GUIDE.md`

## Scope
Define one immutable deployable artifact containing:
- application identity and code version,
- component declarations and minimal entrypoint/runtime metadata,
- Grove Shop application code,
- embedded Web UI static assets,
- a reserved region for customer configuration,
- enough artifact identity metadata to distinguish different immutable artifacts.

The Grove Shop Web component must be able to serve the embedded UI assets from the same Grove deployment. The demo must not require a separate frontend deployment or loose static files.

At this task, a basic embedded page is sufficient. The full live cluster-status experience is completed as later upgrade/read-model capabilities exist.

## Out of scope
OCI registry, signing, remote distribution, Firecracker images, full upgrade/rollback behavior, and polished frontend behavior.

## E2E
During `go test`:
1. build the Grove Shop artifact,
2. inspect its metadata,
3. verify UI assets are contained in the artifact,
4. deploy it to the local cluster,
5. request the Web component root and receive the embedded UI,
6. verify the Grove Shop business flow still works.

## Done
`go test ./...` passes.