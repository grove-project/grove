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

## Scope
Provide source-centered paused layout, locals, stack, goroutines, breakpoints, observed flow, stepping, continue, expression evaluation, source search/navigation, arbitrary source breakpoints, and deterministic debug supervision semantics.

## Acceptance
Hit a Grove breakpoint, inspect state, step/continue, then hit another breakpoint in a service on another node without changing the operator workflow. The order completes after continuing and normal supervision resumes.

## Tests
Exercise Delve-backed state/actions and verify breakpoint pauses never trigger ordinary failure recovery.
