# Grove Task Management

Grove uses repository-native task management. The goal is to make project state understandable from a clean checkout without requiring an external project-management database.

## Hierarchy

```text
Outcome
  -> Workstream
      -> Task
```

### Outcome

An outcome describes an observable project result and its success criteria. Outcomes live in `tasks/outcomes/`.

### Workstream

A workstream is a stable area of engineering responsibility. The directory containing a task is its workstream.

### Task

A task is an executable unit of work with explicit acceptance criteria and dependencies.

## Task metadata

Every new task begins with YAML front matter:

```yaml
---
id: TUI-001
status: in-progress
outcome: groveshop-demo
depends-on:
  - CLUSTER-003
---
```

Required fields:

- `id` — stable task identifier.
- `status` — one of `todo`, `in-progress`, `blocked`, `done`.
- `outcome` — ID of a file under `tasks/outcomes/`.
- `depends-on` — zero or more task IDs.

When `status: blocked`, also add:

```yaml
blocked-by:
  - RUNTIME-004
```

## Task body

Prefer a compact implementation contract:

```markdown
# Cluster topology view

## Goal
What becomes possible when this task is done.

## Scope
What must be implemented.

## Acceptance
Observable conditions proving completion.

## Tests
Automated or manual proof required.
```

Keep examples and acceptance behavior more prominent than long prose.

## Workstreams

Use these stable directories for new tasks:

```text
tasks/
  outcomes/
  runtime/
  cluster/
  networking/
  sdk/
  tui/
  debugging/
  rollout/
  resilience/
  security/
  demo/
  testing/
  docs/
```

A task has one primary workstream. Cross-cutting relationships belong in dependencies rather than duplicating the task.

## Planning horizons

The dashboard groups unfinished work into three horizons:

- **NOW** — at most 3–5 active tasks across Grove.
- **NEXT** — ready or nearly ready work expected after NOW.
- **LATER** — valid work intentionally outside the immediate focus.

Horizon is planning information, not task lifecycle state. Do not invent extra statuses such as `backlog` or `planned`.

## Dependency rules

Dependencies are task IDs, not filenames. A task should not move to `in-progress` while a hard prerequisite is unfinished unless the task explicitly explains why parallel work is safe.

Cycles are invalid.

## Legacy tasks

Tasks 001–032 predate this convention and form the completed MVP implementation history. They remain at their existing paths to preserve links. Do not reorganize them merely for cosmetic consistency.

## Bird's-eye view

`tasks/README.md` is the project dashboard. The long-term implementation should generate it by scanning task front matter and outcome definitions.

The generator/validator should eventually provide equivalents of:

```text
tasks status
tasks next
tasks graph
tasks validate
```

Validation should reject unknown statuses, nonexistent outcome/dependency IDs, duplicate task IDs, dependency cycles, and blocked tasks without a blocker.

## Operating rule

If a task cannot be connected to a current outcome, it should not consume NOW capacity.
