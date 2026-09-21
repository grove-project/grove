---
id: SDK-001
status: todo
outcome: groveshop-demo
depends-on: []
---

# Separate GroveShop application from the Grove runtime/SDK

## Goal

Make the GroveShop demo look and behave like a normal Go application that consumes Grove through public SDK/package boundaries rather than being part of the Grove implementation.

The resulting boundary must make moving GroveShop to its own repository a mechanical extraction rather than an architectural rewrite, while preserving the ability to run and debug the application locally as ordinary Go code.

## Boundary contract

The dependency direction is one-way:

```text
groveshop application
        │
        │ imports public Grove packages
        ▼
   Grove SDK/runtime
```

Grove runtime, SDK, CLI/TUI, cluster, rollout, debugger, and test infrastructure must not import `demo/groveshop` or otherwise depend on GroveShop-specific types, service IDs, configuration structures, status models, or business behavior.

GroveShop owns its application-specific code:

```text
GroveShop
├── domain/business logic
├── application config
├── HTTP/UI surface
├── service registration/wiring
├── application-specific TUI actions/commands
└── main package
        │
        └── Grove public SDK/runtime packages
```

Grove owns only generic capabilities required by applications.

If extracting GroveShop exposes a capability currently implemented with GroveShop-specific knowledge inside Grove, move that capability behind a generic public Grove API instead of preserving the coupling.

## Generic Go application contract

Business logic should remain ordinary Go and should be directly testable/debuggable without starting a Grove cluster.

Grove integration belongs at explicit application composition boundaries. Prefer constructors and ordinary Go calls in domain code; use Grove registration/call APIs where distributed execution is intentionally selected.

A developer must be able to open GroveShop in a Go IDE, set breakpoints in its business logic, and run a local application/test entry point without needing cluster discovery, NATS, placement, rollout, or multi-node orchestration.

The same application code must also be usable through Grove without maintaining a second Grove-specific implementation of the business logic.

## Extraction contract

Treat a future standalone `grove-project/groveshop` repository as the target shape.

This task does not require deleting the in-repository demo immediately, but it must remove repository-internal dependency shortcuts. GroveShop must consume Grove exactly through imports that would remain valid after moving the application to another Go module.

No Grove package may rely on relative co-location, internal Grove packages, or imports back into the GroveShop application.

Add an automated external-module smoke test that proves the boundary: construct/build a temporary Go module containing the GroveShop application shape and consume Grove only through its public packages. The test must fail if extraction would require importing Grove-internal or GroveShop-owned code from the Grove runtime.

## Scope

- Inventory all current imports and type dependencies between `demo/groveshop`, `cmd/grovlet`, `cmd/grove`, console/TUI code, runtime packages, and test helpers.
- Remove Grove runtime/command dependencies on `demo/groveshop`.
- Move application-specific composition, configuration compilation, service registration, status presentation, actions, and demo wiring to the GroveShop side where appropriate.
- Introduce or refine public Grove SDK abstractions only where a genuinely generic capability is required.
- Keep Grove APIs application-agnostic: no GroveShop names, service constants, config structs, or demo assumptions in public/runtime packages.
- Preserve the existing GroveShop demo behavior and the task contracts that depend on it.
- Keep GroveShop business logic runnable and debuggable independently of a cluster.
- Update demo, SDK, CLI/TUI, and implementation documentation where current examples imply Grove owns GroveShop application code.

Do not use this refactor to redesign GroveShop business behavior or introduce unrelated runtime abstractions.

## Human contract

The normal developer experience should read like a generic Go application using a library:

```go
package main

import (
    "github.com/grove-project/grove"
    "github.com/grove-project/grove/sdk"
)

func main() {
    app := newShop()

    // Grove-specific wiring is explicit and stays at the application boundary.
    // Ordinary business code remains ordinary Go.
    _ = app
    _ = grove.ServiceID("")
    _ = sdk.App{}
}
```

The exact public API may differ from this sketch; the contract is the dependency shape and developer experience, not these symbol names.

Local debugging remains ordinary Go tooling:

```text
IDE / dlv
   │
   ▼
GroveShop local process or test
   │
   ├─ ordinary Go business calls
   └─ no cluster bootstrap required
```

Distributed debugging through Grove remains an additional capability, not the only way to debug GroveShop.

## Acceptance

1. No non-demo Grove package or Grove command imports `github.com/grove-project/grove/demo/groveshop`.
2. Grove runtime/SDK APIs contain no GroveShop-specific types, constants, configuration schema, service names, or business assumptions.
3. GroveShop imports only public Grove packages that are valid for an external Go module; it does not import Grove `internal/` packages.
4. GroveShop business logic has a documented local run/test/debug path that does not bootstrap a Grove cluster.
5. The distributed GroveShop binary still uses the same business implementation rather than a duplicated Grove-specific version.
6. Existing GroveShop demo behavior remains intact, including service placement, public ingress, cluster actions/TUI integration, rollout, and debugging flows already implemented at the time this task starts.
7. An automated external-module smoke test proves that the GroveShop application shape can build while consuming Grove as a package without repository-internal coupling.
8. A repository-wide dependency check prevents Grove runtime/SDK/command code from reintroducing imports of the GroveShop demo.
9. Relevant docs and examples describe GroveShop as an application built on Grove, not as part of the Grove runtime.

## Tests

- Run `go test ./...`.
- Add a dependency-boundary test that rejects forbidden Grove -> GroveShop imports.
- Add the external-module extraction/build smoke test.
- Run focused GroveShop unit tests through the cluster-free local path.
- Run the current GroveShop demo/integration tests to prove the refactor did not change distributed behavior.
- Manually attach a normal Go debugger to the local GroveShop path and hit a breakpoint in business logic without starting a cluster.
- Exercise the canonical GroveShop binary after the refactor and confirm its Grove-backed path still works.

If the environment does not permit the local-debug or canonical-demo proof, leave the task unfinished and record the missing verification as required by `tasks/AGENTS.md`.
