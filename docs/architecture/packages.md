# Packages

Every package in the module, what it owns, and what it may not depend on.
Terms are defined in the [glossary](../glossary.md). The dependency rules
are rows in the root `boundary_test.go` ([testing.md](testing.md#architecture-rules)),
and a test there fails if a package is missing from this page.

```text
application binary ─► runtime ─┬─► systemnats ─► controlplane ─► placement
                               ├─► files ─────► placement     (Catalog, Peers ports, implemented by systemnats)
                               ├─► rollout ──► controlplane   (Store port, implemented by systemnats)
                               ├─► inspect ──► controlplane   (Source port, implemented by systemnats)
                               ├─► localcluster ─► nodeproc
                               └─► tui ─► console, consoleview
grove CLI (cmd/grove) ─► inspect, localcluster, scenario, systemnats, controlplane
```

## Application-facing

| Package | Owns | May not depend on |
|---|---|---|
| `grove` (module root) | The SDK every service uses: service and method IDs, the handler registry, `Call` and its client, request envelopes and codecs, `Exclusive` ownership, the Grove Files contract (`Files`, `File`, `OwnedFile`) | any other package in the module |
| `runtime` | The Grovlet, the application runtime and isolated workers, startup hosting, desired-state reconciliation, recovery, and the operator console host. Applications call `runtime.Main(Definition)`. | — |
| `console` | The console's action model: actions, inputs, views and the registry applications add actions to | any other package in the module |

## Domain rules (pure)

| Package | Owns | May not depend on |
|---|---|---|
| `internal/placement` | Every decision about where work runs: `Place`, `FencedEpoch`, `Selector`, `LiveNodes`, `Recover`, `DecideClaim`, `LeaseHolds` | anything but the standard library and `grove` |
| `internal/controlplane` | The control-plane records (membership, health, desired state, deployments, service and handler placement, leases, component status) and the rules for changing them | anything but the standard library, `grove` and `internal/placement` |
| `internal/files` | The Grove Files lifecycle: node disk layout and integrity, sync, fenced ownership, replication, recovery and reconciliation, through `Catalog` and `Peers` ports ([files.md](files.md)) | anything but the standard library, `grove` and `internal/placement` |

## Adapters and orchestration

| Package | Owns | May not depend on |
|---|---|---|
| `internal/systemnats` | The embedded System NATS server, JetStream storage of control-plane records and the Grove Files catalog, watches, and request/reply transport, including the bootstrap witness | — |
| `internal/rollout` | Deployment intent: recording artifacts, activating, proposing a candidate, and health-gated commit or rollback, through a `Store` port | NATS, `internal/systemnats` |
| `internal/inspect` | The read-only view of a cluster, through a `Source` port | NATS, `internal/systemnats` |
| `internal/localcluster` | Starting, stopping and restarting local clusters of node processes for the console, `grove test` and `grove deploy` | — |
| `internal/nodeproc` | Supervising one local Grovlet process and its lifecycle events | — |
| `internal/artifact` | The application artifact envelope: manifest, embedded configuration region and digest | — |
| `internal/bootstrap` | The process-replacement wire contract used to hand a running node over to a new binary | — |
| `internal/debuggateway` | Resolving a service to its process and exposing one Delve DAP session | — |
| `internal/scenario` | The protocol between the `grove` CLI and an application's scenario | — |

## Operator interfaces

| Package | Owns | May not depend on |
|---|---|---|
| `cmd/grove` | The `grove` CLI: status, deploy, test, debug and configuration commands | — |
| `internal/consoleview` | The typed results of the console's read actions | `runtime`, `internal/controlplane`, `internal/systemnats`, `internal/inspect`, `internal/rollout`, `internal/localcluster`, NATS |
| `internal/tui` | The interactive terminal frontend that renders the console model | the same as `internal/consoleview` |

## Tests

| Package | Owns | May not depend on |
|---|---|---|
| `grovetest` | The Go test harness: real Grovlet processes (`Node`, `Cluster`), the in-memory `TestCluster` and `FilesCluster` | — (production may not depend on it) |
| `internal/testbin` | Building test binaries once, on first use, and skipping real-process tests under `-short` | — (only tests import it) |
| `internal/testapp` | The in-repository test application's services | — (production may not depend on it) |
| `internal/testapp/runtimeapp` | The test application's `runtime.Definition` | — |
| `internal/testapp/cmd/testapp` | The test application binary | — |
| `internal/testapp/cmd/handlerapp` | The test application binary with declared handlers on Payment | — |

A dash means no rule restricts the package's dependencies beyond the
module-wide rows: production code never imports `grovetest`,
`internal/testapp`, `internal/testbin` or Grove Shop.
