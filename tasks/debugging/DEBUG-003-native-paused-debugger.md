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

When a breakpoint hits, source becomes dominant and context follows the selected frame:

```text
 GROVE / DEBUG                    PAUSED ●  payment / node-3 / worker-7
----------------------------------------------------------------------------

 service.go                                      LOCALS
------------------------------------------+---------------------------------
  88 func (s *Service) Charge(            | req
  89     ctx context.Context,              | ├─ OrderID    "O-184"
  90     req ChargeRequest,                | ├─ Amount     149.00
▶ 91 ) error {                             | └─ Currency   "USD"
  92     payment := ...                    |
  93                                       | payment
                                           | └─ ...
------------------------------------------+---------------------------------
 payments.Charge · service.go:91 · goroutine 231

 F5 continue  F10 over  F11 into  v locals  s stack  g goroutines  t flow
```

Context panes remain keyboard-first:

```text
 v  locals       expandable structs/maps/slices/pointers
 s  stack        selecting frame updates source + locals
 g  goroutines   select goroutine/frame
 b  breakpoints  Grove + source breakpoints
 t  flow         return to observed Grove call flow
 e  evaluate     Delve expression evaluation
```

Continuing from one node and later stopping on another must look like the same debugging session model:

```text
 orders / node-2   PAUSED ●
       F5
        │
        ▼
 payment / node-3  PAUSED ●
```

No attach-to-host transition is exposed to the operator.

## Scope
Provide source-centered paused layout, locals, stack, goroutines, breakpoints, observed flow, stepping, continue, expression evaluation, source search/navigation, arbitrary source breakpoints, and deterministic debug supervision semantics.

## Acceptance
Hit a Grove breakpoint, inspect state, step/continue, then hit another breakpoint in a service on another node without changing the operator workflow. The order completes after continuing and normal supervision resumes.

## Tests
Exercise Delve-backed state/actions and verify breakpoint pauses never trigger ordinary failure recovery.
