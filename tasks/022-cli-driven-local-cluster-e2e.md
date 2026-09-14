# Task 022 — Application-console local cluster E2E

Status: TODO — reopened after TUI-first design change
Depends on: 021

## Goal
Prove the operator lifecycle through the real Grove Shop application binary and its built-in TUI, with structured actions underneath for deterministic automation.

## E2E
Go test launches 3 Grovlets, starts the real Grove Shop application binary, and proves:

- the application binary can connect to and inspect the cluster;
- the TUI renders cluster health, nodes, services/components, placement, and current version/config identity;
- the operator can drill from application -> service -> worker/node context without switching tools;
- component start/stop where supported is exposed contextually through the TUI;
- at least one application-specific action registered by Grove Shop appears under an Application section;
- the same underlying operations are invokable through structured application-binary actions for deterministic tests;
- the reference application still works after the lifecycle operations.

## TUI contract
The initial information architecture should converge on:

```text
GroveShop
├── Cluster
├── Services
├── Nodes
├── Deployments
├── Configuration
├── Logs
├── Debug
└── Application
```

The implementation may refine labels and layout, but it must preserve application-first navigation and progressive disclosure.

## Design constraints
- The TUI and non-interactive action path must share the same underlying operation/action implementations.
- Do not create parallel business logic for TUI vs automation.
- Human workflows should not require memorizing generic flag trees.
- Headless/runtime execution must not depend on an attached terminal.

## Out of scope
- Deploy/upgrade implementation details beyond navigation placeholders.
- Full debugging flow; Task 032 owns Delve/DAP behavior.

## Done
- A human can operate the local cluster from `./groveshop`.
- Automated tests can invoke equivalent structured actions from the same binary.
- No separately required `grove` CLI remains in this lifecycle.
- `go test ./...` passes.
