# Testing Grove

Grove is tested in four layers. Each layer makes more of the system real, and
each runs slower. Put a test in the lowest layer that can show the behavior.

```bash
go test -short ./...   # layers 1-3, about a minute, builds no binaries
go test ./...          # all four layers, about 13 minutes
```

| Layer | What is real | What is simulated | Where | Runs under `-short` |
|---|---|---|---|---|
| 1. Pure rules | the decision functions | nothing: no I/O, no clock | `internal/placement`, `internal/controlplane`, `internal/rollout`, view builders in `runtime` | yes, in milliseconds |
| 2. Simulated cluster | SDK registry, dispatch and client, plus production's health evaluation, handler reconciliation, leasing and call selection | the store, the network, node processes and the clock | `grovetest.TestCluster` | yes, in milliseconds |
| 3. Embedded NATS | System NATS, JetStream KV, the `systemnats` adapter | node processes (servers run in the test process) | `internal/systemnats` | yes, about 55s |
| 4. Real processes | Grovlet binaries, the `grove` CLI, OS processes, ports and signals | nothing | `runtime`, `cmd/grove`, `grovetest.Node`/`Cluster`, the root `grove` package | no |

## Layer 1: pure rules

Every Grove rule that decides something is a plain function:
- placement, recovery and leases in `internal/placement`
- health, membership, desired state and rollout transitions in
  `internal/controlplane`
- rollout sequencing in `internal/rollout`

Test these with table tests. When you add a rule, add it here first.

## Layer 2: `grovetest.TestCluster`

`TestCluster` runs several logical nodes in one test goroutine on a
controllable clock. It calls production code for everything except storage,
network, processes and time:

| Concern | Production function it runs |
|---|---|
| Failure detection | `controlplane.EvaluateHealth`, `controlplane.Members` |
| Handler placement and epochs | `controlplane.PlanHandlerPlacementsWith`, which calls `placement.Place` |
| Exclusive claims and fencing | `placement.DecideClaim`, `placement.LeaseHolds` |
| Call routing | `placement.Selector` and the real `grove.Dispatcher` |

When production would wait, for example for a previous owner's lease to
expire or for failure detection, the TestCluster advances its clock instead.
Use it for handler placement, failover, partitions and exclusive ownership:

```go
cluster := grovetest.NewTestCluster(t)
n1, n2 := cluster.AddNode(), cluster.AddNode()
n1.Register(charge, chargeHandler)
n2.Register(charge, chargeHandler)
cluster.Start()

cluster.KillNode(n1)
cluster.Converge() // advances past failure detection, then reconciles
cluster.AssertPlacements(charge, n2)
```

Two guards keep it honest:
- a delegation rule in the root `boundary_test.go` fails if the TestCluster
  stops calling the functions above.
- `grovetest/conformance_test.go` replays a scenario through both the
  TestCluster and production reconciliation, and requires identical placements
  and epochs.

Grove Files has its own in-process cluster, `grovetest.FilesCluster`, which
runs production `internal/files` nodes on real directories over an in-memory
catalog and network ([files.md](files.md)).

The TestCluster does not model service placement records and recovery,
desired-state reconciliation, rollout, or execution modes. Test those in
layers 1 and 4.

## Layer 3: embedded NATS

`internal/systemnats` tests start real NATS servers inside the test process.
They cover what only NATS can show: KV compare-and-set, replica growth, watch
behavior and metadata quorum. Keep domain rules out of this layer. Test the
rule in layer 1 and the adapter's storage behavior here.

## Layer 4: real processes

These tests build the test application's Grovlet and the `grove` CLI, start
several OS processes on one host with isolated state and ports, and drive
them as an operator would: start, join, kill, restart, upgrade, roll back and
debug. `AGENTS.md` requires every distributed capability to have one.

Binaries come from `internal/testbin` artifacts. They are built on first use,
once per test binary, and getting one skips the test under `-short`:

```go
var grovletBinary = testbin.New("test Grovlet", func(ctx context.Context, dir string) (string, error) {
	return grovetest.BuildGrovlet(ctx, dir, "./internal/testapp/cmd/testapp")
})

func TestNodeJoins(t *testing.T) {
	node, err := grovetest.StartNode(grovletBinary.Get(t)) // skipped by -short
	...
}
```

A test that starts processes without a built artifact calls
`testbin.RequireProcesses(t)` first. `internal/testbin/testbin_test.go` fails
if a test builds a binary any other way.

Under full-suite load, a few layer-4 tests sometimes time out while the
cluster is still starting. On a failure, rerun that test alone before
treating it as a regression.

## Architecture rules

Every architecture rule is a row in a table in the root `boundary_test.go`.
The rules check non-test code. A test that breaks a rule names the package,
the dependency or the call that broke it.

| Table | A row says | Example |
|---|---|---|
| `importRules` | packages matching `From` never depend on `Forbidden`, even transitively | rollout orchestration does not depend on NATS |
| `pureRules` | a package depends only on the standard library and `Allowed` | placement decisions are pure |
| `delegationRules` | packages matching `Packages` call each function in `Uses` | the TestCluster runs production rules |
| `callRules` | only `Owners` call the functions in `Names` | only the rollout owner writes deployment intent |

In the same file, `TestPresentationReadsThroughInspection` keeps the console,
TUI and CLI reading through `internal/inspect`, and `TestTUIOwnsNoActionNames`
keeps `internal/tui` reading actions from their metadata.

A rule also fails when it stops matching anything, for example after a rename,
so a rule cannot silently guard nothing. To add a boundary:
1. Add a row to the matching table.
2. Break the rule once on purpose and check that the test fails.

One rule is about tests rather than production code, so it lives with what it
protects: `internal/testbin/testbin_test.go` requires real-process tests to
build binaries through `internal/testbin`.

## Continuous integration

`.github/workflows/ci.yml` runs `gofmt`, `go vet ./...` and
`go test -short ./...` on every push and pull request. The full suite runs
nightly and from the workflow's *Run workflow* button.
