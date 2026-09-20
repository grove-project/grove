---
id: ROLLOUT-001
status: todo
outcome: groveshop-demo
depends-on:
  - CLUSTER-001
---

# Detect new build and suggest rollout

## Goal
Turn the direct edit -> build -> run loop into Grove's deployment experience.

## Scope
When a started binary discovers a cluster with the same application identity but a different build identity, present **Roll out this build** instead of ordinary node join. The candidate process introduces the artifact but must not silently increase the intended stable node count.

## Acceptance
Build a modified GroveShop binary and run it beside the existing cluster. The TUI identifies it as a rollout candidate and requires only confirmation to begin rollout.

## Tests
E2E same-app/same-build => join, same-app/different-build => rollout candidate, different-app => unrelated.
