# Operating Grove

**Grove should explain what the application is doing, what changed, and how the runtime is responding.**

Operators are Grove's second primary audience after developers. The goal is a smooth operational model built around the application—not a requirement to reconstruct application state from unrelated infrastructure layers.

The operational tool is the application binary itself:

```bash
$ ./groveshop
```

## Start with the cluster

```text
Cluster

Health      healthy
Version     v0.8.2
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Config      production-42
```

Then drill down only when needed:

```text
Services > orders

Health       healthy
Instances    3 / 3 healthy
Placement    node-1, node-2, node-3
Version      v0.8.2
Config       production-42
Restarts     0

[Logs] [Executions] [Placement] [Debug]
```

## A failure should tell a story

```text
Cluster

Health      degraded

orders      degraded
  instance on node-2 failed after configuration update

Recovery
  production-43 → production-42
  rollback in progress
```

Drilling into the service should reveal the timeline without switching tools:

```text
Services > orders > Timeline

10:41:02  production-43 deployed
10:41:04  node-2 became unhealthy
10:41:06  process restarted
10:41:09  process exited again
10:41:10  rollout stopped
10:41:10  rollback initiated
10:41:13  production-42 healthy

Result
  recovered in 11s
```

This is the target operational experience: **state + change + cause + action + result**.

## Observability is evidence plus explanation

Logs, metrics, traces, health signals, placement state, configuration history, deployment history, and runtime events are evidence. Grove should correlate the evidence it owns into an intuitive view of the application.

The TUI should expose the raw evidence contextually:

```text
Services > orders
  ├─ Overview
  ├─ Timeline
  ├─ Logs
  ├─ Traces
  ├─ Placement
  ├─ Executions
  └─ Debug

Deployments
  ├─ Current artifact
  ├─ Rollout history
  └─ Configuration history
```

For scripts and automation, the same information is available through structured actions from the same application binary:

```bash
./groveshop action service.logs orders
./groveshop action service.inspect orders
./groveshop action cluster.nodes
./groveshop action rollout.status
./groveshop action config.history
./groveshop action diagnose
```

Routine diagnosis should not begin by manually joining those sources together.

## Operations are part of the application lifecycle

```text
Deploy → Observe → Diagnose → Recover → Verify
   ↑                                  │
   └──────────────────────────────────┘
```

The same Grove model should cover local clusters, production clusters, and customer-edge nodes. Where a service runs may change; the way an operator understands it should not.

## Operational principles

- **The application carries its own operational console.** No separate generic CLI is required for normal operation.
- **Interactive first.** Human workflows are live, contextual, and navigable through the TUI.
- **Scriptable underneath.** Structured actions preserve CI and automation use cases.
- **The runtime explains itself.** Grove exposes decisions and transitions, not only telemetry.
- **Application first.** Operators reason about services, versions, configuration, health, and changes before infrastructure internals.
- **Failures have timelines.** Recent changes and recovery actions should be easy to correlate.
- **Recovery is observable.** Automatic action must never mean invisible action.
- **Drill-down is progressive.** Start with a concise system view and expose deeper evidence on demand.
- **Developer and operator models agree.** The service a developer writes is the service an operator inspects.