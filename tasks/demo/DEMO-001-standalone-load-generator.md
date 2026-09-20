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

## Scope
Build a standalone demo binary that drives GroveShop through its public ingress. Support deterministic start/stop and configurable request rate. Track request totals, successes, failures, latency, and current throughput. It must not depend on Grove internals to generate business traffic.

## Acceptance
Run the load generator against the same GroveShop ingress while nodes fail/recover and builds roll out. It continues generating traffic and exposes the resulting load state.

## Tests
Deterministic integration test against GroveShop ingress including failure/recovery and rollout transitions.
