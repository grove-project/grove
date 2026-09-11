# Plan: Operate Grove through the CLI

## Goal

Complete Task 021 by adding the smallest useful `grove` command that reports
cluster status, lists nodes and components, and starts or stops a component
through the existing System NATS control endpoints. Prove the real CLI binary
can inspect a three-Grovlet cluster without implementing Task 022's complete
operator lifecycle.

## Context

Tasks 001 through 020 are complete on `origin/main`. Grovlets already expose
machine-readable membership/health and component lifecycle endpoints through
`internal/systemnats.Transport`. The new command may consume those existing
control APIs but must not introduce deployment packaging, upgrades, rollout
state, a TUI, or a competing control protocol.

### Key Files

- `tasks/021-grove-cli-lifecycle.md` — current CLI acceptance contract.
- `cmd/grove/main.go` — new command entry point, parser, control calls, and
  human-readable output.
- `cmd/grove/main_test.go` — parser/output tests and real-binary cluster E2E.
- `internal/systemnats/health.go` — node health read model consumed by status
  and nodes commands.
- `internal/systemnats/component.go` — component read and lifecycle operations
  consumed by components and component start/stop commands.
- `grovetest/grovetest.go` — real Grovlet process harness reused by the E2E.

### Decisions Made

- Use explicit per-command connection flags: `--system-nats-url` selects the
  control-plane connection and `--node-id` selects the observer or lifecycle
  target. Component actions additionally require `--service-id`.
- Keep output compact and human-readable. `status` aggregates node and
  component health; `nodes` and `components` emit deterministic tables; a
  lifecycle action prints the resulting component row.
- Connect directly to existing Grovlet control endpoints through System NATS.
  Do not add HTTP, a new public SDK abstraction, or duplicated cluster state.
- Task 021's E2E invokes real `grove status` against three real Grovlets. Task
  022 retains ownership of the full CLI stop/start and Grove Shop call flow.

## Sub-Tasks

- [ ] 1. Parse CLI commands and render control-plane reads.
  **Context:** Implement `status`, `nodes`, and `components` with typed command
  validation, deterministic output, and actionable errors. Query cluster state
  from the requested observer and query component views for known nodes.
  **Acceptance:** Parser/error and rendering tests cover valid and invalid
  invocations without requiring a running cluster.

- [ ] 2. Control component lifecycle through the CLI.
  **Context:** Implement `component start` and `component stop` using the
  existing request/reply lifecycle endpoints. Require a non-zero stable service
  ID and print the resulting component state.
  **Acceptance:** Unit tests prove the selected action, node, and service ID are
  sent to the control client and failures reach stderr/exit handling.

- [ ] 3. Exercise the real CLI against real Grovlets.
  **Context:** Build `grove` and `grovlet`, start three isolated Grovlet OS
  processes with production-shaped clustered System NATS, condition-wait by
  invoking the CLI until status reports the healthy cluster, and clean up every
  process with diagnostics on failure.
  **Acceptance:** No fixed sleeps or manual setup are required; the E2E proves
  the real CLI binary observes three healthy logical nodes.

- [ ] 4. Verify and close Task 021.
  **Context:** Run formatting, diff checks, focused repetitions, vet, the full
  uncached suite, and the full race suite before marking the task DONE.
  **Acceptance:** All checks pass without weakening historical tests.

## Log

- 2026-09-11: Tasks 001 through 020 are complete on `origin/main`.
- 2026-09-11: Reserved the complete CLI-driven component lifecycle and Grove
  Shop verification scenario for Task 022 while retaining every Task 021
  command in the implemented grammar.
