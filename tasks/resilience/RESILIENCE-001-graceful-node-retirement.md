---
id: RESILIENCE-001
status: done
outcome: groveshop-demo
depends-on:
  - CLUSTER-001
---

# Graceful node retirement and service recovery

## Goal

Leaving a GroveShop terminal should remove that logical node and restore every
service on surviving nodes instead of leaving permanent unavailable membership
and placement records.

## Human contract

Starting four copies and quitting a service-hosting node should converge without another
operator action:

```text
Before                         After node-2 exits
Nodes      4 / 4 healthy       Nodes      3 / 3 healthy
Services   5 / 5 healthy       Services   5 / 5 healthy
Cluster    HEALTHY             Cluster    HEALTHY
```

The surviving views may show a bounded recovering transition while Inventory
and Shipping move, but must not retain a phantom node or require a replacement
process merely to become healthy.

## Scope

- Retire a gracefully stopped node from authoritative JetStream/KV membership.
- Keep failed nodes observable when a process is killed rather than gracefully stopped.
- Make every same-build joined Grovlet capable of hosting each GroveShop service.
- Reuse the existing health, placement, component lifecycle, and System NATS
  recovery coordinator to relocate services before retirement completes.
- Preserve the cluster Web address when Web is relocated on the same demo host.

## Out of scope

- Automatic forgetting of abruptly failed nodes.
- Stateful migration, capacity-aware scheduling, and multi-host ingress routing.
- Redesigning the Cluster TUI; TUI-001 owns the richer recovery timeline.

## Acceptance

Start one GroveShop artifact four times, gracefully stop the first node,
and observe every survivor converge on three healthy members, five healthy
placements, and a successful order through the original Web address.

## Tests

Add deterministic unit coverage for authoritative membership retirement and a
real-process E2E for four-node graceful leave, service relocation, shared-state
convergence, stable ingress, and post-recovery application success.
