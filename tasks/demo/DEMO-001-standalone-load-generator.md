---
id: DEMO-001
status: todo
outcome: groveshop-demo
depends-on:
  - NET-001
---

# Standalone GroveShop load generator

## Goal
Keep realistic visible traffic flowing while cluster actions are demonstrated.

## Human contract

The load generator is a separate demo binary so it can stay running in its own terminal while GroveShop nodes are manipulated:

```text
./bin/groveshop-load --target http://127.0.0.1:8080
```

Startup should enter the load TUI directly. Non-interactive automation may use explicit flags/actions, but the demo should not require Grove-internal addresses or APIs.

Conceptually:

```text
GroveShop public ingress
        ▲
        │ ordinary application requests
        │
groveshop-load
```

The generator must behave like an external client. Cluster failure/recovery and rollout effects are therefore real user-visible effects, not synthetic control-plane metrics.

## Scope
Build a standalone demo binary that drives GroveShop through its public ingress. Support deterministic start/stop and configurable request rate. Track request totals, successes, failures, latency, and current throughput. It must not depend on Grove internals to generate business traffic.

## Acceptance
Run the load generator against the same GroveShop ingress while nodes fail/recover and builds roll out. It continues generating traffic and exposes the resulting load state.

## Tests
Deterministic integration test against GroveShop ingress including failure/recovery and rollout transitions.
