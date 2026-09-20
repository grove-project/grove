---
id: ROLLOUT-002
status: todo
outcome: groveshop-demo
depends-on:
  - ROLLOUT-001
  - NET-001
---

# Config changes use the artifact rollout path

## Goal
Make configuration deployment indistinguishable from code deployment at the cluster lifecycle level.

## Scope
Embed changed customer configuration into a new immutable artifact/build identity. Starting it must use the same discovery, candidate, health-gating, stable-ingress, cutover, and rollback flow as a code change.

## Acceptance
Run a GroveShop artifact containing broken Inventory configuration. Grove offers the normal rollout, detects the unhealthy candidate, rejects/rolls it back, and restores the previous complete known-good artifact without changing ingress.

## Tests
Deterministic config-only candidate E2E using the same rollout machinery as code-only candidates.
