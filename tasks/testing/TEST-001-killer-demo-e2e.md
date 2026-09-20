---
id: TEST-001
status: todo
outcome: groveshop-demo
depends-on:
  - TUI-001
  - ROLLOUT-002
  - DEBUG-003
  - DEMO-002
---

# Killer GroveShop demo end-to-end proof

## Goal
Turn the complete GroveShop story into one deterministic acceptance contract.

## Scope
Compose bootstrap, same-binary joins, shared cluster observation, service placement, order flow, continuous load, node failure/recovery, code rollout, stable ingress, config-only failed rollout/rollback, runtime-guided debugging across nodes, and final healthy order execution.

## Acceptance
The documented canonical demo can be executed from a clean checkout without hidden setup or implementation-specific operator knowledge.

## Tests
Automate everything that does not require visual TUI inspection. No fixed sleeps, Docker, or shell orchestration for acceptance. Keep a concise manual checklist for the visual/debug interaction proof.
