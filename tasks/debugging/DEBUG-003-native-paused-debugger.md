---
id: DEBUG-003
status: todo
outcome: groveshop-demo
depends-on:
  - DEBUG-002
---

# Native paused debugger experience

## Goal
Make distributed Go debugging usable entirely from the Grove application TUI while Delve remains the debugging engine.

## TUI contract

When a breakpoint hits, source becomes dominant and Locals is the default context:

```text
 GROVE / DEBUG                    PAUSED ●  payments / node-3 / worker-7
──────────────────────────────────────────────────────────────────────────────

 service.go                                     LOCALS
─────────────────────────────────────────┬────────────────────────────────────
  88 func (s *Service) Charge(           │ req *ChargeRequest
  89     ctx context.Context,             │ ├─ OrderID    "O-184"
  90     req ChargeRequest,               │ ├─ Amount     149.00
▶ 91 ) error {                            │ └─ Currency   "USD"
  92     payment := newPayment(req)       │
  93                                      │ payment *Payment
  94     err := s.gateway.Charge(...)     │ ├─ Status     "pending"
                                          │ └─ ...
─────────────────────────────────────────┴────────────────────────────────────
 payments.Charge · service.go:91 · goroutine 231
──────────────────────────────────────────────────────────────────────────────
 F5 continue  F10 over  F11 into  ⇧F11 out  v locals  s stack  g goroutines
```

### Locals

`v` focuses Locals. `j/k` selects values and `Enter` expands/collapses structs, pointers, slices, maps, and nested values.

### Stack

```text
 CONTEXT / STACK
────────────────────────────────────────
 > payments.Charge          service.go:91
   payments.Handle          handler.go:72
   grove.rpc.invoke         rpc.go:182
   grove.worker.run         worker.go:94
────────────────────────────────────────
 j/k frame   Enter/open   v locals
```

Selecting a frame updates both source and locals to that frame.

### Goroutines

```text
 CONTEXT / GOROUTINES
────────────────────────────────────────
 > 231  stopped   payments.Charge
   114  waiting   nats.(*Conn).readLoop
    88  waiting   runtime.gopark
────────────────────────────────────────
 j/k select   Enter stack
```

### Breakpoints

```text
 GROVE / DEBUG / BREAKPOINTS
──────────────────────────────────────────────────────────────────────────────
 ◆ orders.CreateOrder       service.go:41     all instances
 ◆ payments.Charge          service.go:91     all instances
 ● gateway.go:143           payments          source breakpoint
──────────────────────────────────────────────────────────────────────────────
 j/k select   Enter source   Space enable/disable   x remove   Esc back
```

### Evaluate

```text
┌─ Evaluate ──────────────────────────────────────────────┐
│ > req.Amount * 1.17                                    │
│                                                       │
│ 174.33                                                │
└───────────────────────────────────────────────────────┘
```

`e` evaluates through Delve. Grove does not implement Go expression semantics.

### Return to observed flow

```text
 TRACE / OBSERVED FLOW
────────────────────────────────────────────────────────
 POST /orders
 │
 ├─ web.PlaceOrder              node-1
 ├─ orders.CreateOrder          node-2
 ├─ inventory.Reserve           node-1
 └─ payments.Charge             node-3   ← current
────────────────────────────────────────────────────────
 j/k select   Enter source   Space breakpoint
```

`t` opens this context. Flow -> source and paused source -> flow navigation must both work.

### Cross-node continuation

Continuing from one node and later stopping on another must feel like one application debugging workflow:

```text
orders.CreateOrder
node-2 / worker-3
      │
      │ F5 continue
      ▼
payments.Charge
node-3 / worker-7
```

The TUI changes source, locals, stack, node, and worker context automatically. No attach-to-host transition is exposed.

### Keyboard contract

```text
j/k          navigate
h/l          pane / collapse / expand
Enter        open / follow / expand
Space        toggle breakpoint
Ctrl-P       source/symbol search
@            current-file symbols
/            source search
b            breakpoints
t            trace / observed flow
s            stack
v            locals
g            goroutines
e            evaluate
F5           continue
F10          step over
F11          step into
Shift-F11    step out
Alt-Left     navigation back
Alt-Right    navigation forward
```

## Scope
Provide the source-centered paused layout, expandable locals, stack/frame navigation, goroutines, Grove + arbitrary source breakpoints, observed-flow navigation, stepping, continue, expression evaluation, source search/navigation, and deterministic debug supervision semantics.

The native TUI and external DAP frontend must share Grove's debug manager/target-resolution infrastructure. Delve remains responsible for Go breakpoints, variables, stack, goroutines, evaluation, and stepping.

Synthetic cross-service instruction-level step-into is not required.

## Acceptance
Hit a Grove breakpoint, inspect/expand application state, switch stack frames and see source+locals follow, evaluate an expression, step/continue, return to the observed flow, then hit another semantic breakpoint in a service on another node without changing the operator workflow.

The order completes after continuing and normal supervision resumes. A debugger pause never causes Grove to migrate/restart the intentionally stopped worker.

## Tests
Exercise Delve-backed state/actions and verify:
- locals/stack/goroutine data comes from the intended worker;
- frame selection updates source and locals;
- expression evaluation reaches Delve;
- breakpoint pauses never trigger ordinary failure recovery;
- continuing can subsequently hit a semantic breakpoint resolved to another service/node;
- external DAP attachment continues to work through the same debug infrastructure.
