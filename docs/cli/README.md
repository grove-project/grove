# Grove Application Console

**Every Grove application is its own operational tool.**

Grove does not require a separate `grove` executable for normal application operation. The Grove runtime, operational console, debugging entry points, configuration tooling, and developer-defined administrative actions are compiled into the application binary itself.

```text
groveshop
├── application code
├── Grove runtime
├── Grove operational actions
├── developer-defined application actions
└── interactive TUI
```

The primary human experience is an interactive terminal UI, not a large tree of generic flags.

Grove Shop implements this experience now: its root invocation opens the
application console, and `action` exposes the same registered handlers for
automation.

## Start the application console

```bash
$ git clone https://github.com/grove-project/groveshop && cd groveshop
$ go build -o ./bin/groveshop ./cmd/groveshop
$ ./bin/groveshop
```

A Grove-aware application opens its interactive console when invoked in an interactive terminal:

```text
Grove Grove Shop
Cluster  healthy
Nodes    3 / 3 healthy
Services 3 / 3 healthy
Version  v0.1.0
Config   acme-r42

App
  Run integrity check  [app.orders.verify]

Cluster
  Status  [cluster.status]

Services

Debug
  Attach debugger  [debug.attach]

Logs
  View logs  [logs.view]
```

The same executable may also start the runtime directly when used as the deployed process. TUI activation must never make headless execution depend on a terminal.

## Application-specific actions

Developers can register actions from application code. Grove merges them into the same operational surface as built-in runtime actions.

Conceptually:

```go
app := grove.New("groveshop")

app.Service("orders", orders.Run)
app.Service("payment", payment.Run)

app.Action("Seed demo orders", seedOrders)
app.Action("Generate load", generateLoad)
app.Action("Run integrity check", verifyOrders)

app.Run()
```

The important property is not the exact API shape. The application action runs with the application's own packages, types, embedded configuration, credentials, and Grove connectivity. Developers should not need to build and distribute a second admin utility.

## TUI first, structured actions underneath

The TUI is a view over a structured action registry:

```text
TUI
 ↓
Action registry
 ├── cluster.status
 ├── service.inspect
 ├── config.inspect
 ├── debug.attach
 ├── rollout.start
 ├── app.orders.seed
 └── app.orders.verify
```

This separation gives Grove two interfaces without two products:

- **Humans:** interactive, live, contextual, discoverable TUI.
- **Automation:** stable structured actions exposed by the same application binary.

A non-interactive form may look like:

```bash
$ ./bin/groveshop action cluster.status
$ ./bin/groveshop action app.orders.verify
```

The exact action syntax is intentionally secondary to the TUI. Grove documentation should prefer TUI examples for human workflows and use action invocations only for scripts, CI, tests, or reproducible automation.

## Inspect the cluster

From the TUI:

```text
Cluster

Health      healthy
Version     v0.8.2
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Config      production-42

Last event
  orders recovered after config rollback
```

A healthy system should be boring. When it is not healthy, the same view should explain the reason and the runtime response:

```text
Cluster

Health      degraded
Services    2 / 3 healthy

orders      degraded
  node-2 restarted 3 times in 42s

Last change
  config production-43 deployed 48s ago

Recovery
  restoring production-42
```

## Drill into a service

```text
Services > orders

Health       degraded
Instances    2 / 3 healthy
Version      v0.8.2
Config       production-43

Cause
  max_connections: -1

Current action
  restoring production-42

[Logs] [Executions] [Placement] [Debug]
```

The goal is not to replace raw logs, metrics, or traces. It is to make Grove's own state and decisions understandable before the operator has to correlate lower-level evidence manually.

## Debug where the code actually runs

```text
Services > orders > Instances > node-2 > Debug

Target       orders / node-2
Worker       worker-17
State        ready

[Attach debugger]
```

After attach:

```text
DAP endpoint
  127.0.0.1:4711

Waiting for IDE...
```

Grove resolves placement; the developer debugs the application. The same model applies when the target runs on another cluster node or customer edge.

For automated debugger setup, the same application binary may expose the underlying action directly:

```bash
$ ./groveshop action debug.attach orders --listen 127.0.0.1:4711
```

## Change configuration safely

Configuration remains part of immutable artifact identity. The console should present the operation as a rollout, not mutation of a running process.

```text
Deployments > New candidate

Artifact     groveshop v0.8.3
Config       production-43

[Start rollout]
```

During the rollout:

```text
Rollout production-43

✓ node-1 updated
✗ orders failed health check

Rollout stopped
Restoring production-42...
✓ cluster healthy
```

The important output is **what changed, what Grove observed, what Grove did, and whether the system recovered**.

## Design rules

### One binary
The application binary is the user-facing operational executable. Grove should not require operators to match an external CLI version to an application/runtime version.

### Interactive by default, scriptable underneath
The TUI is the primary human interface. Stable structured actions exist underneath for automation and reproducibility.

### Application-aware
The console can expose both Grove runtime capabilities and application-specific operations registered by the developer.

### Live and contextual
Cluster state, rollout progress, worker placement, health, logs, recovery, and debugger targets should update in place where useful rather than forcing repeated polling commands.

### Progressive disclosure
Start from the application and service. Reveal worker, Grovlet, node, routing, storage, and control-plane details only when useful.

### The runtime explains itself
Every operational surface should help answer:

- What is running?
- What changed?
- What is unhealthy?
- Why did it happen?
- What is Grove doing about it?
- Did recovery succeed?

Raw runtime detail remains available for deeper investigation, but it should not be the first thing a human must decode.

## Core principle

> **The same binary you build and deploy is also the tool you use to inspect, operate, debug, and administer the application.**

That extends Grove's "same binary from dev to prod" principle beyond packaging: the artifact carries its own operational vocabulary with it.
