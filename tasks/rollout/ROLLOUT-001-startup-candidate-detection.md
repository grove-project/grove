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

## TUI contract

When a different build of the same application starts:

```text
┌─ GroveShop ─────────────────────────────────────────────┐
│ Existing GroveShop cluster discovered                  │
│                                                        │
│ Cluster         groveshop-local                        │
│ Running build   a82f19c                                │
│ This build      c41de72                                │
│                                                        │
│ This looks like a new version of GroveShop.            │
│                                                        │
│ › Roll out this build                                  │
│   Exit                                                  │
│                                                        │
│ Enter select                                           │
└────────────────────────────────────────────────────────┘
```

After confirmation, the initiating process becomes a rollout observer/participant rather than an accidental fourth stable node:

```text
 GROVESHOP / DEPLOYMENT                           ROLLING OUT ◐

 a82f19c  ───────────────────────────────>  c41de72

 Candidate health     HEALTHY
 Services migrated    3 / 5
 ████████████████████████░░░░░░░░░░░░  60%

 The application ingress remains unchanged.

 [Enter] details   [q] leave view
```

## Scope
When a started binary discovers a cluster with the same application identity but a different build identity, present **Roll out this build** instead of ordinary node join. The candidate process introduces the artifact but must not silently increase the intended stable node count.

## Acceptance
Build a modified GroveShop binary and run it beside the existing cluster. The TUI identifies it as a rollout candidate and requires only confirmation to begin rollout. Existing node TUIs simultaneously expose the rollout through their shared Cluster view.

## Tests
E2E same-app/same-build => join, same-app/different-build => rollout candidate, different-app => unrelated.
