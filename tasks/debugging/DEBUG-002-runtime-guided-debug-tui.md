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

## Scope
Implement `Debug > Last flow`. Show executed registered operations and current placement. Allow opening the embedded source for an operation and creating a semantic Grove breakpoint that resolves through Grove's existing Delve/debug manager.

## Acceptance
Place an order, open Last flow, choose Orders and Payment operations on different nodes, create Grove breakpoints without finding files/PIDs/ports, then hit both during a subsequent order.

## Tests
Automated semantic target-resolution tests plus the manual native-TUI sequence in `demo/DEBUGGING_DEMO.md`.
