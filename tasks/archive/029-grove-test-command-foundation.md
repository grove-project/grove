# Task 029 — Application-native test action foundation

Status: TODO — reopened after application-console design change
Depends on: 022, 023

## Goal
Expose Grove's developer testing experience through the application binary while Go tests remain the underlying automation mechanism.

The human experience should be reachable from the TUI, for example:

```text
Testing > Run E2E
Testing > Run resilience
```

The same operation must be available as a structured action from the application binary for CI and automation:

```bash
./groveshop action test.run
```

## Scope
- Orchestrate the reference application's Grove E2E flow and return deterministic exit status.
- Surface progress/results in the TUI.
- Expose the same operation through the structured action registry.
- Keep Go tests as the implementation/automation substrate.

## Out of scope
Resilience matrix, SLA language, test generation.

## Tests
Exercise the real Grove Shop application binary from Go tests and prove the TUI operation and structured action share the same execution path.

## Done
- No standalone `grove test` executable contract is required.
- TUI and action invocation both execute the Grove application test flow.
- `go test ./...` passes.
