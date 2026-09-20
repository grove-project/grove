---
id: DEBUG-002
status: todo
outcome: groveshop-demo
depends-on:
  - DEBUG-001
---

# Runtime-guided native debugging TUI

## Goal
Allow an operator unfamiliar with GroveShop source to move from a real request directly to meaningful breakpoint targets.

## TUI contract

The primary entry point is an observed application flow, not a process list:

```text
 GROVESHOP / DEBUG / LAST FLOW

 Order O-184 · completed 2s ago

 web.PlaceOrder                         node-1
      │
      ▼
 orders.CreateOrder                    node-2
      │
      ├──> inventory.Reserve           node-1
      │
      ├──> payments.Charge             node-3
      │
      └──> shipping.CreateShipment     node-2

 ↑↓ select   Enter source   Space Grove breakpoint   t trace
```

Selecting an operation gives semantic context before source-level details:

```text
 payments.Charge                                      node-3

 Service       payment
 Operation     payments.Charge
 Worker        worker-7
 Build         c41de72
 Source        payment/service.go:88

 › Open source
   Add Grove breakpoint
   Attach external DAP…
```

A Grove breakpoint is displayed separately from an arbitrary source breakpoint:

```text
 ◆ Grove breakpoint    payments.Charge
 ● Source breakpoint   payment/service.go:103
```

The operator never chooses a PID, node address, or Delve port.

## Scope
Implement `Debug > Last flow`. Show executed registered operations and current placement. Allow opening the embedded source for an operation and creating a semantic Grove breakpoint that resolves through Grove's existing Delve/debug manager.

## Acceptance
Place an order, open Last flow, choose Orders and Payment operations on different nodes, create Grove breakpoints without finding files/PIDs/ports, then hit both during a subsequent order. The examples above define the information hierarchy; exact rendering may adapt to terminal size.

## Tests
Automated semantic target-resolution tests plus the manual native-TUI sequence in `demo/DEBUGGING_DEMO.md`.
