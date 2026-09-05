# Grove Shop MVP Implementation Guide

## Purpose
This file tells implementation agents how the demo contracts map onto the numbered Grove tasks.

The `demo/` folder defines the target behavior. The `tasks/` folder remains the source of truth for what may be implemented in each increment.

Do not implement the whole demo at once.

## Incremental mapping

### Foundation
- Task 005: create Grove Shop business-domain code only.

### Runtime
- Tasks 006-010: progressively wire Grove Shop service calls through Grove's explicit service registry, invocation envelope, System NATS transport, and cross-node dispatch.
- Keep the Orders business flow unchanged while replacing local plumbing with Grove runtime plumbing.

### Cluster awareness and recovery
- Tasks 011-020: place Grove Shop components across real Grovlets, expose/derive cluster state, detect failures, recover components, and preserve desired deployment state.
- The demo UI should not be used as a source of truth. All state it eventually displays must come from Grove's structured control-plane read model.

### Developer workflow and artifact
- Task 021-022: keep CLI interaction minimal and suitable for the final two-command demo.
- Task 023: define one immutable artifact containing application code, embedded UI assets, deployment metadata, and reserved config region.
- Task 024: implement customer config embedding/extraction and support distinct artifact identity when config differs.

### Upgrade and rollback
- Tasks 025-028: treat the complete artifact as the versioned deployment unit. Introduce candidate state, health gating, cutover, and rollback.
- A candidate with broken Inventory config must fail deterministically and cause Grove to return to the previous complete known-good artifact.

### Final MVP proof
- Tasks 029-031: automate the exact lifecycle in `DEMO_FLOW.md` using Go tests and the Grove test harness.
- Final acceptance must prove order success before the bad deployment and again after rollback.

## UI implementation guidance
The Web UI is part of Grove Shop and must be served by a Grove-managed Web component.

The final page has two panes on one screen:
- Orders UI.
- Cluster Status.

The Cluster Status pane continuously polls a structured Grove read-model endpoint approximately every 500 ms to 1 second.

Use polling for MVP v1. Do not introduce WebSockets/SSE unless a later task explicitly changes the requirement.

The UI must be able to observe candidate rollout and rollback without controlling them.

## Minimal CLI target
The final demo should converge on:

```bash
grove deploy --config configs/acme.yaml
grove deploy --config configs/acme-broken.yaml
```

Do not force the user through separate cluster-create, config-compile, config-embed, artifact-create, or upgrade commands for the headline demo. Lower-level commands may exist for diagnostics/testing.

## Scope discipline
Do not add debugging/DAP to MVP v1. That belongs to a later demo evolution.

Do not add storefront complexity, authentication, external payment providers, databases, or frontend frameworks merely to make the sample feel realistic. The demo should remain deterministic, fast, and easy to E2E test.