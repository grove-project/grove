# Grove Documentation

Grove documentation starts with the experience: **what you write, what you run, and what Grove tells you.** Architecture comes later, when you want to understand how the runtime delivers those guarantees.

## Build with Grove

```go
type Inventory struct{}

func (s *Inventory) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
    // Ordinary Go business logic.
}
```

Register the distributed boundary explicitly and keep the business package directly testable.

→ **[Grove SDK](../sdk/)** — services, invocation, serialization, capabilities, and the canonical Shop example.

## Work with Grove

The application binary is also the shared operational surface for developers and operators.

```bash
$ ./groveshop
```

```text
┌ GroveShop ────────────────────────────────────────────┐
│ Cluster: healthy     Nodes: 3      Version: v0.8.2   │
├───────────────────────────────────────────────────────┤
│ > Services                                            │
│   Nodes                                               │
│   Deployments                                         │
│   Configuration                                       │
│   Logs                                                │
│   Debug                                               │
│                                                       │
│ ─ Application ─                                       │
│   Seed demo orders                                    │
│   Run integrity check                                 │
└───────────────────────────────────────────────────────┘
```

Humans use the TUI. Scripts, CI, and reproducible automation invoke the same structured actions through the same application binary:

```bash
$ ./groveshop action cluster.status
```

→ **[Application Console](cli/)** — TUI, structured actions, debugging, rollouts, and application-specific operations.

## Operate Grove

Grove should explain the system, not merely expose telemetry.

```text
Services > orders

Health        degraded
Cause         config production-43
Observed      2 crash-looping instances
Action        rollback to production-42
Result        recovered
```

→ **[Operations](operations/)** — health, observability, changes, diagnosis, and recovery.

## Understand Grove

**[Concepts](concepts.md)** defines Grove's mental model: services, workers, Grovlets, nodes, clusters, ingress, RPC, control/data planes, placement, configuration, durable execution, edge, and operations.

**[Vision](vision/vision.md)** explains why Grove exists and the product principles behind the experience.

**[Architecture](architecture/system-architecture.md)** explains how Grove implements the runtime.

**[ADRs](adr/README.md)** capture important architectural decisions and their trade-offs.

**[Roadmap](roadmap/roadmap.md)** shows where the project is going.

**[MVP demo](mvp-demo/README.md)** turns the core experience into one end-to-end scenario.

## Documentation rule

> **Show the experience. Explain only what the example cannot.**

For human workflows, prefer TUI examples over generic command/flag trees. Use direct action invocations only where automation, CI, testing, or reproducibility is the point.

See the **[documentation guide](DOCUMENTATION_GUIDE.md)** for the project-wide writing standard.