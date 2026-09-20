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

## Canonical story

The final proof must preserve this order so the demo reads as one continuous developer/operator experience:

```text
start first GroveShop binary
        ↓
start same binary twice more → join same 3-node cluster
        ↓
shared Cluster TUI shows placement across nodes
        ↓
place order → distributed flow succeeds
        ↓
edit GroveShop and build a new binary with a visible feature
        ↓
start different build → rollout suggested for same logical cluster
        ↓
watch rollout in all Cluster views; ingress URL/port stays unchanged
        ↓
use the new feature
        ↓
kill one node
        ↓
services relocate to eligible surviving nodes
        ↓
place another order → upgraded app still succeeds
        ↓
Debug > Last flow
        ↓
observed grove.Call graph → registered operation → embedded source
        ↓
create semantic Grove breakpoints
        ↓
repeat order → source + locals + stack
        ↓
continue → breakpoint in service on another node
        ↓
order completes; cluster healthy
```

Continuous load from the standalone loader/TUI should make rollout and failure transitions observable where appropriate without replacing the explicit order actions above.

## Scope
Compose bootstrap, same-binary joins, shared cluster observation, service placement, order flow, continuous load, code rollout with a visible new feature, stable ingress, node failure/recovery and relocation, config-only failed rollout/rollback, runtime-guided debugging across nodes, and final healthy order execution.

The debugging portion must exercise the native TUI contracts from DEBUG-001/002/003:
- captured registered Grove call flow from a real request;
- current placement shown with that flow;
- operation -> exact-build embedded source navigation;
- semantic Grove breakpoint creation;
- paused source + locals;
- stack/frame navigation;
- goroutine visibility;
- expression evaluation;
- observed-flow navigation;
- continue into another service currently hosted on another node;
- no PID/host/Delve-port discovery by the operator.

External DAP remains an interoperability/automation proof, not the headline human demo.

## Acceptance
The documented canonical demo can be executed from a clean checkout without hidden setup or implementation-specific operator knowledge.

The same browser endpoint remains usable across rollout and recovery. The new feature is visible after rollout and remains available after node loss/recovery.

During debugging, the operator starts from the order that just executed rather than from repository/process knowledge, reaches source through the observed Grove flow, and debugs across at least two currently differently placed services without reconnecting to another debugger endpoint.

## Tests
Automate everything that does not require visual TUI inspection. No fixed sleeps, Docker, or shell orchestration for acceptance.

Keep a concise manual checklist for visual/debug interaction proof covering:
1. synchronized Cluster views;
2. visible new feature after rollout on the same ingress endpoint;
3. visible service relocation after node loss;
4. observed Debug flow;
5. operation-to-source navigation;
6. Grove breakpoint creation;
7. paused source/locals/stack;
8. cross-node breakpoint transition;
9. final successful order and healthy cluster.
