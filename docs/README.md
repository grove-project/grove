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

→ **[Grove SDK](../sdk/)** — services, invocation, serialization, and the canonical Shop example.

## Work with Grove

The CLI is the shared surface for developers and operators.

```bash
$ grove status

Cluster     healthy
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Version     v0.8.2
Config      production-42
```

→ **[CLI](cli/)** — run, inspect, test, debug, deploy, and recover.

## Operate Grove

Grove should explain the system, not merely expose telemetry.

```bash
$ grove inspect orders

orders        degraded
Cause         config production-43
Observed      2 crash-looping instances
Action        rollback to production-42
Result        recovered
```

→ **[Operations](operations/)** — health, observability, changes, diagnosis, and recovery.

## Understand Grove

**[Vision](vision/vision.md)** explains why Grove exists and the product principles behind the experience.

**[Architecture](architecture/system-architecture.md)** explains how Grove implements the runtime.

**[ADRs](adr/README.md)** capture important architectural decisions and their trade-offs.

**[Roadmap](roadmap/roadmap.md)** shows where the project is going.

**[MVP demo](mvp-demo/README.md)** turns the core experience into one end-to-end scenario.

## Documentation rule

> **Show the experience. Explain only what the example cannot.**

Feature docs should normally lead with code, commands, output, or a diagram. Prose should clarify guarantees, trade-offs, and internals rather than make readers imagine the experience.

See the **[documentation guide](DOCUMENTATION_GUIDE.md)** for the project-wide writing standard.