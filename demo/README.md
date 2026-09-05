# Grove Shop MVP Demo

## Purpose
Grove Shop is the permanent reference application for the Grove MVP. It exists to prove Grove's core developer and runtime lifecycle in one visible, self-contained demo.

The demo must feel like a real application first and an infrastructure demo second.

## Core story
A developer builds one Grove application artifact containing:
- the application services,
- the embedded Web UI,
- Grove runtime metadata,
- and a reserved customer-configuration section.

A customer configuration is embedded into the artifact after compilation. The artifact is deployed onto a local multi-Grovlet cluster. The browser shows the business application and Grove cluster state side by side. A second artifact is produced with a deliberately bad customer configuration. Grove starts it as an upgrade candidate, detects that a component crashes, rejects the candidate, and returns to the previous known-good artifact.

## What the MVP demo must prove
1. Normal Go application code using Grove's explicit service model.
2. One immutable deployment artifact contains app code, Web UI, runtime metadata, and embedded customer configuration.
3. Multiple real Grovlet processes form a cluster on one host using production-shaped transport.
4. Services communicate locally and across Grovlets.
5. System NATS carries transient control traffic.
6. JetStream/KV holds authoritative replicated Grove control state.
7. The application UI is served by a Grove-managed component from the same cluster.
8. The UI continuously polls Grove state and renders upgrades and recovery live.
9. A bad embedded configuration can make a candidate component fail.
10. Grove detects the failed candidate and automatically restores the previous known-good deployment.
11. The business application is healthy after rollback.

## Minimal operator flow

```bash
grove deploy --config configs/acme.yaml

# Keep the browser open.

grove deploy --config configs/acme-broken.yaml
```

The browser should be enough to observe the full upgrade, failure, and rollback sequence.

## Supporting docs
- `ARCHITECTURE.md` — demo services, artifact composition, and runtime topology.
- `UI.md` — single-screen Orders + Cluster Status contract.
- `CONFIGURATION.md` — embedded config and intentional failure scenario.
- `DEMO_FLOW.md` — exact end-to-end demo sequence and expected observable states.
- `IMPLEMENTATION_GUIDE.md` — how the demo contracts map onto the numbered tasks without violating incremental scope.

## Non-goals for MVP v1
- Debugger/DAP integration.
- Firecracker live migration.
- Kubernetes integration.
- Edge-specific deployment.
- Sophisticated application behavior.
- Production storefront features.

Debugging is a natural MVP v2 extension, but must not complicate the first demo.