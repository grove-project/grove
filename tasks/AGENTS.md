# Agent Guide for Grove Tasks

This file is the operating guide for coding agents working under `tasks/`.

## Start here

Before implementing anything:

1. Read `tasks/README.md` for the current outcome, NOW/NEXT/LATER horizons, and dependency shape.
2. Read `tasks/TASK_MANAGEMENT.md` for the task format and lifecycle rules.
3. Read the target task file completely.
4. Read its dependency tasks and the relevant implementation/docs before changing code.
5. Treat the task spec as the implementation contract. If code, docs, tests, and the task disagree, resolve the inconsistency rather than silently choosing one.

## Selecting work

Prefer tasks in **NOW** whose hard dependencies are complete.

Do not start a dependency-gated task merely because it is interesting. A task may move to `in-progress` only when its prerequisites are satisfied, unless the task explicitly documents why parallel work is safe.

Work on one coherent task at a time. Avoid opportunistic refactors outside its scope.

## While implementing

Keep the implementation aligned with Grove's existing architecture and terminology. Reuse established abstractions instead of creating parallel concepts.

For human-facing behavior, the task's TUI/CLI/Human contract is authoritative. Preserve:

- information hierarchy;
- terminology;
- available actions and keyboard interactions;
- meaningful state transitions;
- operator-visible behavior.

ASCII screens are behavioral contracts, not pixel-perfect layouts. Adapt rendering to terminal size without changing the experience they specify.

Do not leak implementation details such as NATS addresses, worker PIDs, or node-local Delve ports into the user experience unless a task explicitly requires them.

## Tests are part of the task

Implement the proof described by `## Tests` and `## Acceptance`, not only the production code.

Before marking a task done:

1. Run focused tests for the changed components.
2. Run relevant integration/E2E tests when the task crosses component boundaries.
3. Exercise human-facing flows against the task's example screens/transcripts.
4. Confirm every acceptance criterion is observable and satisfied.
5. Do not claim a command or demo flow works unless it was actually exercised when the environment permits it.

If a required test cannot be run, leave the task unfinished and record the reason.

## Updating task state

Task lifecycle is:

`todo -> in-progress -> blocked -> done`

When beginning real implementation, update the task to `in-progress`.

If blocked, set `status: blocked`, add `blocked-by`, and document enough context for the next agent to continue without rediscovering the blocker.

Set `status: done` only after implementation, tests, acceptance criteria, and required documentation are complete.

Keep `tasks/README.md` consistent with task state and dependencies. Do not invent new lifecycle statuses.

## Documentation

A task is not complete when it changes a documented concept or demo behavior but leaves the repository docs stale.

Update relevant docs in the same change. In particular, keep demo guides, implementation guides, concepts, and examples synchronized with user-visible behavior.

When changing TUI behavior, update all related task contracts/screens so the repository does not describe contradictory experiences.

## Scope discipline

Do not rewrite completed legacy tasks `001`–`032` for formatting consistency. They are implementation history and existing links must remain stable.

Do not duplicate cross-cutting work into several task files. Give it one primary workstream and express relationships through dependencies.

If implementation reveals genuinely new work, create a focused follow-up task with proper metadata rather than silently expanding the current task.

## Completion handoff

A completed task should leave the repository in a state where another agent can understand:

- what changed;
- why it changed;
- how it is verified;
- what user/operator experience is expected;
- what task should logically follow.

The goal is not merely to make code compile. The goal is to advance the current Grove outcome with a tested, documented, demonstrable increment.
