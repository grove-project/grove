# Grove Documentation Guide

Grove documentation should feel like the product: compact, explicit, understandable, and centered on the application.

## Audience

Grove has two primary documentation audiences:

1. **Developers** building applications with Grove.
2. **Operators** running, observing, diagnosing, upgrading, and recovering Grove applications.

Developer experience is not a documentation category. It is a quality bar for every user-facing page. Operations is a first-class product surface and should be demonstrated alongside development whenever a feature has runtime consequences.

## The rule

> **Show the experience. Explain only what the example cannot.**

Prefer:

```text
code → command → output → short explanation
```

over:

```text
concept → long explanation → architecture → example
```

As an editorial heuristic, a user-facing guide should be dominated by code, commands, realistic output, and diagrams rather than prose.

## Start from the user's perspective

A feature page should usually answer these questions in this order:

```text
What can I do?
      ↓
What does my code look like?
      ↓
What does the CLI experience look like?
      ↓
What happens operationally?
      ↓
How do I know what Grove is doing?
      ↓
What does Grove guarantee?
      ↓
What are the trade-offs?
      ↓
How does it work internally?
```

Not every page needs every section. The ordering is more important than the template.

## Use one recurring application

Use **Grove Shop** as the canonical example across the docs. Reuse `api`, `orders`, and `inventory` instead of inventing unrelated `foo` and `bar` examples.

The reader should progressively learn the same application:

```text
SDK             build it
CLI             run it
Configuration   change it
Testing         test it
Resilience      break it
Observability   understand the failure
Recovery        watch Grove recover
Migration       upgrade it
Edge            place part of it at the edge
```

Complete runnable examples belong in the repository; documentation should reuse excerpts from them.

## Show failure paths

Grove's value is often clearest when something goes wrong. Do not document only successful deployment.

Show:

```text
change → failure → detection → explanation → recovery → verification
```

A good failure example tells the reader what Grove observed, why Grove believes it happened, what action it took, and whether recovery succeeded.

## Make the runtime explain itself

Avoid vague claims such as "Grove provides intuitive observability." Demonstrate the intended experience instead:

```bash
$ grove inspect orders

Cause     config production-43
Action    rollback to production-42
Result    recovered
```

Metrics, logs, traces, runtime events, configuration history, and placement are evidence. User-facing docs should show how Grove turns the evidence it owns into understandable state, transitions, and causality.

## Progressive disclosure

Keep three levels of depth:

```text
Experience
    ↓
Concept / guide
    ↓
Architecture / ADR
```

A developer should not need to understand consensus, NATS internals, placement algorithms, or process supervision before using a feature. Link to architecture for readers who want the mechanism.

## State trade-offs

Mature documentation says where a feature does **not** fit. Significant capabilities should state boundaries and costs explicitly.

Example:

> Durable execution is designed for resumable workflows. It is not intended for latency-sensitive request paths.

## Keep prose short

A reader should be able to understand the main value of a page in roughly 30 seconds by scanning its headings, examples, output, and diagrams.

Delete prose that merely repeats what an example already makes obvious.

## Separate current behavior from intended behavior

Grove is under active development. Never make an unimplemented interface look shipped.

When a page demonstrates a target experience, label it clearly. When behavior is implemented, examples should be kept executable or tested where practical.

## Review checklist

Before merging a user-facing documentation change, ask:

- Can the value be understood by skimming the examples?
- Does the page start with the user experience rather than internals?
- Does it show both development and operational consequences where relevant?
- Does it use the canonical Grove Shop vocabulary where possible?
- Does it demonstrate a failure path when recovery is part of the feature's value?
- Are guarantees and trade-offs explicit?
- Are deep implementation details linked rather than front-loaded?
- Is it clear which examples describe implemented behavior versus intended behavior?

If the examples tell the story without most of the prose, the page is doing its job.