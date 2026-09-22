# Issue Grooming Process

Grove uses GitHub Issues as the evolving units of work. An issue may begin as a small idea and mature into an implementation contract.

This document is the operating contract for both humans and the automated grooming agent. The grooming agent runs daily, scans the repository issues, and aligns them with this process.

## Principles

1. Capture ideas cheaply. An issue does not imply a commitment to implement it.
2. Preserve provenance. Consolidate related ideas rather than losing the reasoning that produced them.
3. Groom progressively. Do not require implementation detail while an idea is still exploratory.
4. `stage:ready` means the issue is safe to implement without inventing important product or architecture decisions.
5. Ready is a contract. Once work starts, do not silently move the target.
6. Durable architecture and product knowledge belongs in `docs/`; issues describe work and its evolution.
7. The grooming agent may organize and enrich existing intent, but must not invent product decisions where the repository or issues do not provide enough evidence.

## Lifecycle

```text
idea
  ↓
consolidating
  ↓
specifying
  ↓
ready
  ↓
in-progress
  ↓
done
```

Use exactly one lifecycle label on every open work issue:

- `stage:idea`
- `stage:consolidating`
- `stage:specifying`
- `stage:ready`
- `stage:in-progress`

A closed completed issue represents `done`. Closed ideas may instead be duplicates, superseded, rejected, or absorbed into another issue; the closing comment must say which.

Area/outcome labels may be added independently, for example `area:cluster`, `area:sdk`, `area:debugging`, or `outcome:groveshop-demo`.

## 1. Idea

### Purpose

Capture an insight immediately without forcing premature design.

An idea needs only enough context to understand the desired capability or problem. Missing implementation details are expected.

### Example

**Title:** Allow a Grovlet to advertise hardware capabilities

**Labels:** `stage:idea`, `area:placement`

**Body:**

> Some services may require a GPU or another hardware capability. A Grovlet should expose enough node capability information for placement to take this into account.

This issue should not yet contain invented APIs, wire formats, or implementation tasks.

### Grooming-agent behavior

- Add `stage:idea` when an unclassified issue is clearly an early idea.
- Add obvious area/outcome labels when supported by the issue.
- Search open and recently closed issues for overlapping intent.
- Link likely related issues.
- Do not promote merely because the idea sounds useful.

## 2. Consolidating

### Purpose

Combine ideas that describe the same underlying work into one canonical issue.

The canonical issue should preserve the useful requirements and reasoning from the source issues. Source issues are then closed with a reference to the canonical issue.

### Example

Suppose these exist:

- #21 — GPU-aware service placement
- #34 — Services should declare required node hardware
- #41 — Placement should understand accelerator availability

They all concern the same capability.

Create or select a canonical issue:

**#45 — Hardware-aware service placement**

Move the useful requirements from #21, #34, and #41 into #45. Link the source issues, then close them with comments such as:

> Consolidated into #45. The GPU requirement described here is preserved there as an example of hardware-aware placement.

The canonical issue gets `stage:consolidating` while its boundaries are still being resolved.

### Grooming-agent behavior

The agent should identify strong semantic overlap, but consolidation is destructive enough to require confidence.

It may automatically consolidate when issues are clearly equivalent and no contradictory requirements exist. When intent differs materially or the relationship is uncertain, link the issues and leave a grooming comment rather than guessing.

Never close an issue as consolidated without linking the canonical issue.

## 3. Specifying

### Purpose

Turn a consolidated capability into a sufficiently precise implementation contract.

This is where Grove-specific design knowledge is attached to the work.

A specifying issue should progressively answer:

- Why is this needed?
- What behavior is required?
- Which existing Grove concepts constrain it?
- Which components are affected?
- What is explicitly out of scope?
- What dependencies exist?
- What should the TUI/UX look like when relevant?
- What tests prove the behavior?
- Which documentation must change?

### Example

The hardware-placement issue might evolve into:

**Goal**

Allow service placement validation to reject nodes that do not provide required hardware.

**Required behavior**

- Grovlets expose locally observed capabilities.
- Placement validation remains service-defined.
- A service with no placement restrictions remains eligible everywhere.
- Capability changes cause placement eligibility to be reevaluated.

**Out of scope**

- Grove does not provide GPU scheduling policy in this task.
- Grove does not install device drivers.

**Acceptance criteria**

- [ ] A service requiring a GPU cannot be placed on a node without one.
- [ ] An unrestricted service remains eligible on all healthy nodes.
- [ ] Deterministic E2E coverage exists.

The issue remains `stage:specifying` while important decisions or acceptance criteria are missing.

### Grooming-agent behavior

Use repository documentation, related issues, completed work, and linked PRs to enrich the specification.

Do not fabricate unresolved architecture. If an implementation-significant question cannot be answered from existing Grove knowledge, leave it explicit under:

```markdown
## Open questions
- Should capability changes trigger immediate migration or only affect future placement?
```

An issue with implementation-significant open questions is not ready.

## 4. Ready

### Purpose

`stage:ready` means an implementation agent or developer can pick up the issue and execute it without making important product or architecture decisions.

Ready is the implementation contract.

### Definition of Ready

Before promotion, verify that the issue has, where applicable:

- clear goal and motivation;
- required externally observable behavior;
- relevant architecture/SDK constraints;
- scope and meaningful out-of-scope boundaries;
- dependencies and related issues;
- TUI/UX contract when user-visible behavior is involved;
- acceptance criteria;
- test expectations;
- documentation impact;
- no unresolved implementation-significant open questions.

Not every issue needs every heading. The criterion is whether the missing information would force the implementer to invent an important decision.

### Example

**CLUSTER-001 — Interactive same-binary discovery and join** becomes ready only after it defines first-node behavior, same-build discovery, the join TUI, application/build identity expectations, isolation from unrelated applications, acceptance criteria, and deterministic E2E expectations.

At that point:

```text
stage:specifying → stage:ready
```

### Grooming-agent behavior

Promote to `stage:ready` only when the Definition of Ready is satisfied.

The agent should be conservative here. A false-ready issue is worse than an issue remaining in specifying for another day.

## 5. In Progress

### Purpose

Work has started against the frozen ready contract.

Normally this means a branch or PR exists, an implementation agent has claimed the issue, or there is another explicit indication that implementation started.

### Example

#45 is picked up and PR #62 implements hardware-aware placement.

Update:

```text
stage:ready → stage:in-progress
```

The PR should reference the issue.

### Contract-change rule

A new idea must not silently rewrite an in-progress issue.

Create the idea separately and link it to the active work. Groom its impact into one of three cases.

#### A. Required for correctness

Example: while implementing cluster discovery, we discover that unrelated applications can collide on application identity.

This changes correctness of the active contract.

- mark the new issue `impact:active-work`;
- link it to the in-progress issue;
- amend the active issue explicitly;
- add a `## Spec changes` entry describing what changed and why;
- adapt the implementation before completion.

#### B. Valuable follow-up

Example: while implementing same-LAN discovery, someone proposes discovery across isolated network domains.

If the current implementation remains correct without it, keep the active contract unchanged. The new idea follows the normal grooming lifecycle as follow-up work.

#### C. Invalidates the implementation approach

Example: a newly established architecture constraint makes the approach currently being implemented unsafe.

- mark the active issue blocked;
- move it back to `stage:specifying` if the contract itself must be redesigned;
- consolidate the new information;
- establish a new ready contract;
- resume implementation only after it is ready again.

### Grooming-agent behavior

Every daily run should specifically inspect new or changed issues for relationships to `stage:in-progress` work.

Potential impact should be surfaced quickly. The agent must not silently broaden active scope.

## 6. Done

### Purpose

The implementation contract has been satisfied and the work is integrated.

An issue can close as completed when its acceptance criteria are satisfied and the implementing change is merged or otherwise present on the target branch.

### Example

PR #62 merges, all hardware-placement acceptance criteria are covered, and required docs are updated. Close #45.

The closing context should make the implementation easy to trace, normally through the linked PR.

## Daily Grooming Agent

The scheduled ChatGPT grooming agent should perform one repository-wide pass per day.

### Scan

Inspect all open issues and relevant recently changed/closed issues. Read issue bodies, labels, relationships, comments, linked PRs, and relevant repository documentation before changing lifecycle state.

### Align

For every open issue:

1. Ensure it has exactly one lifecycle stage.
2. Add supported area/outcome classifications.
3. Find duplicates and overlapping ideas.
4. Consolidate clear duplicates while preserving provenance.
5. Enrich `stage:specifying` issues from existing Grove knowledge.
6. Promote issues that satisfy the Definition of Ready.
7. Detect whether new ideas affect in-progress work.
8. Detect in-progress issues whose implementation appears completed and verify acceptance criteria before closure.
9. Correct stale links, contradictory issue text, or lifecycle labels when evidence is clear.

### Safety boundaries

The grooming agent should autonomously perform clerical and evidence-backed alignment, but it must not manufacture Grove product decisions.

When evidence is insufficient:

- keep the issue at its current pre-ready stage;
- record the unresolved question;
- link relevant context;
- avoid closing or materially redefining issues based on weak similarity.

For in-progress work, prefer surfacing a possible contract change over silently editing the contract.

### Daily report

After each run, summarize only meaningful changes, for example:

```text
Grove issue grooming

Promoted to ready
- #45 Hardware-aware service placement

Consolidated
- #34 and #41 → #45

Needs decision
- #53: whether capability loss triggers immediate migration

Active-work impact
- #57 may affect #4 CLUSTER-001; linked but active contract unchanged

No action
- 18 issues already aligned
```

The report should make human attention cheap: emphasize promotions, consolidations, unresolved decisions, and changes affecting active work rather than listing every issue scanned.

## Key invariant

> An Issue may evolve freely before it is ready. Once ready work begins, its contract changes only explicitly.

This allows Grove to capture ideas aggressively while keeping implementation deterministic and safe for both humans and coding agents.
