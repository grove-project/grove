---
id: DEBUG-002
status: todo
outcome: groveshop-demo
depends-on:
  - DEBUG-001
---

# Runtime-guided native debugging TUI

## Goal
Allow an operator unfamiliar with GroveShop source to move from a real request directly to meaningful application code and breakpoint targets.

## TUI contract

The primary entry point is an observed application flow, not a process list:

```text
 GROVE / DEBUG / LAST FLOW                         POST /orders · 184ms
──────────────────────────────────────────────────────────────────────────────

 EXECUTED GROVE CALLS                         SUGGESTED BREAKPOINTS
───────────────────────────────────────┬──────────────────────────────────────
 web.PlaceOrder             node-1     │ [ ] web.PlaceOrder
       │                               │ [◆] orders.CreateOrder
       ▼                               │ [◆] inventory.Reserve
 orders.CreateOrder         node-2     │ [◆] payments.Charge
       │                               │
       ├──── inventory.Reserve node-1  │
       │                               │
       └──── payments.Charge   node-3  │
───────────────────────────────────────┴──────────────────────────────────────
 j/k navigate   Enter source   Space breakpoint   a recommended   t trace
```

The node assignments are illustrative; the screen renders current observed placement.

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

### Source screen

```text
 GROVE / DEBUG / SOURCE                         orders · node-2 · worker-3
──────────────────────────────────────────────────────────────────────────────

 FILES / SYMBOLS              internal/orders/service.go
─────────────────────┬────────────────────────────────────────────────────────
 orders              │  39   order := newOrder(req)
   service.go        │  40
   repository.go     │◆ 41   if err := s.reserve(ctx, order); err != nil {
                     │  42       return nil, err
 payments            │  43   }
   service.go        │  44
   gateway.go        │▶ 45   receipt, err := s.charge(ctx, order)
                     │  46
 inventory           │
   service.go        │
─────────────────────┴────────────────────────────────────────────────────────
 orders.CreateOrder · service.go:45 · BP:1
──────────────────────────────────────────────────────────────────────────────
 j/k move  Space breakpoint  Enter follow  / search  Ctrl-P source  @ symbols
```

Source navigation is application-oriented, not directory-oriented:
- `Ctrl-P` fuzzy-searches files, packages, services, types, functions, and methods;
- `@` lists symbols in the current file;
- `/`, `n`, `N` search source;
- `:<line>`, `gg`, `G` navigate lines;
- `Alt-Left` / `Alt-Right` navigate history;
- `Enter` follows resolvable symbols and registered `grove.Call` destinations;
- `Space` toggles a breakpoint.

A selected `grove.Call` should jump directly to the registered destination implementation instead of Grove transport plumbing.

### Breakpoint identities

```text
 ◆ Grove breakpoint    payments.Charge
 ● Source breakpoint   payment/service.go:103
```

A Grove breakpoint is semantic: service + registered operation. Grove resolves it to the active artifact, current worker, symbol/source location, and Delve breakpoint. It is not fundamentally tied to a node or line number.

The operator never chooses a PID, node address, or Delve port.

## Scope
Implement `Debug > Last flow`, runtime-guided breakpoint suggestions, operation-to-source navigation using the embedded exact-build source bundle, keyboard-first source navigation, and semantic Grove breakpoint creation through Grove's existing debug manager.

Source-code protection/encryption is explicitly out of scope for this demo task.

## Acceptance
Place an order, open Last flow, choose Orders and Payment operations on different nodes, open their source, create Grove breakpoints without finding files/PIDs/ports, then hit both during a subsequent order.

The operator can begin debugging without knowing the repository structure.

The screens above define information hierarchy and interaction contracts; exact rendering may adapt to terminal size.

## Tests
Automated semantic target/source-resolution tests plus the manual native-TUI sequence in `demo/DEBUGGING_DEMO.md`. Verify a Grove breakpoint still resolves correctly when the same service is placed on another eligible node for a subsequent run.
