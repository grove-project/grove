# Testing with Grove

**What you test locally should be what you ship.**

A Grove application should be testable as the same self-contained application/runtime shape later used in deployment.

```bash
$ grove test

baseline                 passed
worker-crash             passed
node-loss                passed
service-restart          passed
upgrade-and-rollback     passed
```

> The CLI above describes the intended Grove experience while the MVP implementation is still evolving.

## Developer contract

Developers define the business flow and correctness assertions. Grove should reuse those flows to exercise runtime failures rather than require a separate chaos-test suite.

```text
E2E flow
   ↓
healthy baseline
   ↓
Grove observes participating services and dependencies
   ↓
relevant failures are injected
   ↓
the same assertions verify recovery
```

Tests may optionally define latency or availability expectations. Exact SLOs are not required before a test becomes useful.

## Version-bound validation

Each Grove release owns the E2E contract that validates that release. During a multi-version state migration Grove should validate each adjacent hop with the target version's own tests:

```text
migrate N → N+1
      ↓
boot N+1
      ↓
run N+1 E2E
      ↓
checkpoint success
```

A migration function returning successfully is not enough. The target application should prove it can operate correctly on the resulting state.

## Implementation testing rules

For contributor-level testing requirements—real Grovlet processes, production-shaped transport, bounded condition waits, diagnostics on timeout—see [`AGENTS.md`](../../AGENTS.md).