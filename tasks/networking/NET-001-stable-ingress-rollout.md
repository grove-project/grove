---
id: NET-001
status: todo
outcome: groveshop-demo
depends-on:
  - ROLLOUT-001
---

# Stable ingress through rollout and rollback

## Goal
A Grove rollout changes application implementation without changing application network identity.

## Scope
Keep the Grove-managed ingress address and port stable while old and new builds coexist, during cutover, and during rollback. Candidate startup must not replace the public listener.

## Acceptance
Keep one GroveShop browser/client connected to the same endpoint while a rollout completes and while a failed candidate rolls back. Existing behavior remains reachable and new behavior becomes visible after cutover.

## Tests
Capture ingress before rollout and assert identical endpoint plus successful requests before, during mixed-version state, after cutover, and after rollback.
