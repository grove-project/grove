# Agent-Assisted Service Migration

## Purpose

Grove should provide an agent skill that migrates **one existing Kubernetes-hosted Go service at a time** into Grove.

The defining requirement is not merely that the migration works. The diff between the original service and the Grove-hosted service should be **as small as reasonably possible**.

> **Preserve the service. Change the runtime boundary.**

Migration must not become an excuse to redesign or refactor the application.

## Primary contract: minimal source diff

The migration agent optimizes for:

1. preserving business/domain code unchanged;
2. preserving the service's external contract;
3. preserving persistence semantics;
4. preserving existing tests;
5. changing primarily runtime wiring, startup, configuration adapters, and transport boundaries;
6. keeping interoperability with services that have not yet migrated;
7. producing the smallest understandable diff that makes the service run under Grove.

Any required change to business logic should be treated as suspicious and explicitly reported.

A successful migration should ideally look like:

```text
Business files changed:       0
Runtime/wiring files changed: small
External API compatibility:   preserved
Existing tests:               passing
Legacy dependencies:          supported
Legacy callers:               supported
```

Diff size is itself a migration quality signal and should be reported by the agent.

## Example

Before:

```go
func main() {
    cfg := loadConfig()

    svc := inventory.New(cfg)

    http.HandleFunc("/reserve", svc.ReserveHTTP)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

After migration, the desired shape is approximately:

```go
func main() {
    cfg := loadConfig()

    svc := inventory.New(cfg)

    grove.Register("inventory", svc)
    grove.Run()
}
```

The `inventory` implementation should remain unchanged unless Grove cannot preserve the required behavior through an adapter or runtime-boundary change.

## Migration flow

For a selected service, the agent should first inspect and describe the current runtime boundary:

- Go entrypoint;
- Kubernetes Deployment;
- Kubernetes Service and exposed ports;
- readiness/liveness probes;
- CPU and memory requests/limits;
- ConfigMaps, Secrets, environment variables, and other configuration inputs;
- callers;
- outbound service dependencies;
- persistence dependencies;
- node/LAN/locality constraints;
- existing unit and end-to-end tests.

It should then classify every relevant dependency/caller:

```text
dependency already in Grove
    → Grove-native communication may be used when it does not unnecessarily enlarge the migration diff

dependency still in Kubernetes
    → preserve the existing network endpoint

caller still in Kubernetes
    → preserve the existing external contract through the Kubernetes Service / Grove ingress compatibility boundary
```

The agent then performs the smallest changes required to execute the service under Grove.

## Compatibility first

The first migration pass should favor compatibility over Grove-specific optimization.

For example, if a service currently uses an HTTP client to call `payment.default.svc.cluster.local`, migrating `inventory` should not require replacing that client merely because Grove has a native RPC mechanism.

The existing client can remain until `payment` is migrated and there is a clear reason to optimize the communication path.

This keeps migration independent of the service dependency graph.

## Two distinct stages

Grove must distinguish migration from optimization.

### Stage 1 — compatibility migration

Goal: run the existing service under Grove with minimal behavioral and source changes.

```text
same business logic
same API
same persistence behavior
same legacy dependencies
same callers
same tests
        ↓
runs under Grove
```

### Stage 2 — optional Grove optimization

Only after the compatibility migration is healthy should a developer or agent propose Grove-native improvements such as:

- Grove-native service RPC;
- local/in-process call optimization;
- durable execution;
- Grove persistence primitives;
- storage locality;
- placement hints/validation;
- configuration-model improvements;
- topology-aware scheduling.

These changes must be separate from the migration itself so their benefits and risks can be reviewed independently.

## Changes the migration agent should avoid

During the compatibility migration, do not opportunistically:

- redesign domain models;
- rename public APIs;
- change request/response schemas;
- rewrite persistence code;
- introduce unrelated abstractions;
- restructure packages;
- modernize unrelated code;
- convert every dependency to Grove RPC;
- add Grove design patterns that are not necessary for execution.

Prefer a small adapter or wrapper at the runtime boundary over invasive changes to service internals.

## Migration result

Before applying or presenting the final migration, the agent should summarize it in a compact report such as:

```text
Service:                    inventory
Current runtime:            Kubernetes Deployment
Target runtime:             Grove
Business files changed:     0
Runtime/wiring files:       3
Lines changed:              42
External API:               preserved
Existing tests:             passing
Legacy callers:             preserved
Legacy dependencies:        payment
Grove dependencies:         orders
Placement constraints:      none
Cutover:                    K8s Service → Grove ingress
```

The exact fields may evolve, but the report must make **migration invasiveness** immediately visible.

## Validation

A migration is successful only when the service behaves equivalently under the same relevant tests/workload.

The agent should validate, where available:

- existing unit tests;
- existing integration/end-to-end tests;
- startup and health behavior;
- external API compatibility;
- persistence behavior;
- connectivity to unmigrated dependencies;
- connectivity from unmigrated callers.

The migration should also integrate with the Kubernetes resource-utilization demo so resource usage can be compared before and after moving the service into Grove.

## Future CLI experience

The agent workflow could eventually be exposed through a command such as:

```bash
grove migrate service inventory
```

The command/agent should discover the service, propose or apply the minimal compatibility migration, validate it, and report the resulting diff and compatibility status.

## Design principle

The migration experience should communicate a simple idea:

> **Migrating to Grove should feel like changing how the service runs, not rewriting what the service does.**

This is essential to Grove's gradual Kubernetes adoption story. A team should be able to migrate one service, measure the result, and continue only when ready.