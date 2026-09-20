# Task 021 — Application-native Grove console foundation

Status: TODO — reopened after application-console design change
Depends on: 013, 016

## Goal
Move Grove's operational surface into the application binary itself and establish the structured action registry that powers both interactive and non-interactive operation.

The application must not require a separately installed `grove` executable for normal operation.

## Scope
- Add a Grove action registry that application binaries can embed.
- Register built-in actions for cluster status, nodes, components/services, and component start/stop where appropriate.
- Allow application code to register application-specific actions into the same registry.
- Expose a non-interactive application-binary entry point for structured actions, conceptually:

```bash
./groveshop action cluster.status
./groveshop action service.inspect orders
```

- Ensure action implementations operate against public Grove control APIs rather than CLI-specific internals.
- Keep the action model suitable for a TUI frontend in Task 022.

## Design constraints
- One application binary contains application code, Grove runtime integration, and operational actions.
- Built-in and application-specific actions share one registry and lifecycle.
- Action names/arguments must be stable enough for tests and automation.
- The action layer is not itself the primary human UX; it is the scriptable substrate beneath the TUI.

## Out of scope
- Full TUI implementation; Task 022 owns that.
- Deployment packaging and upgrades.
- General-purpose plugin systems.

## Tests
- Unit-test action registration, lookup, argument validation, and errors.
- E2E invokes the real Grove Shop application binary against a `grovetest` cluster and exercises built-in actions.
- E2E registers at least one Grove Shop-specific action and proves it is visible/invokable through the same registry.

## Done
- No separate `grove` executable is required by the tested operator lifecycle.
- Built-in and application actions are reachable through the application binary.
- `go test ./...` passes.
