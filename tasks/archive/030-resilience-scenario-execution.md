# Task 030 — Resilience scenario execution

Status: TODO — reopened after application-console design change
Depends on: 018, 029

## Goal
Run one controlled resilience action while a developer-defined E2E flow executes, exposed through the Grove Shop application console rather than a separate generic CLI.

## Scope
MVP action: kill the node hosting a selected stateless service; wait for Grove recovery; rerun functional flow.

Human flow:

```text
Testing > Resilience > Node failure
```

Automation flow from the same application binary:

```bash
./groveshop action test.resilience
```

The TUI and structured action must share the same underlying scenario implementation.

## Out of scope
Full resilience matrix, SLA enforcement, network partitions, disk corruption, CPU throttling.

## E2E
The application-native resilience workflow proves the flow before failure, injects node death, waits for recovery, and proves the flow again.

## Done
- The resilience scenario is reachable from the TUI.
- The same scenario is scriptable from the Grove Shop binary.
- `go test ./...` passes.
