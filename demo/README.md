# Grove Shop MVP Demo

## Purpose
Grove Shop is the permanent reference application for the Grove MVP. It proves Grove's developer, operator, and runtime lifecycle in one visible, self-contained demo.

The demo must feel like one distributed application rather than a collection of infrastructure processes.

## Core story
A developer builds one Grove Shop application artifact containing application services, embedded Web UI, Grove runtime/operational surface, deployment metadata, and embedded customer configuration.

Running the artifact is the primary human workflow:
- the first instance discovers no compatible cluster and offers **Start new cluster**;
- another instance of the same application and exact artifact discovers the cluster and suggests **Join**;
- a different artifact of the same application discovers the cluster and suggests **Roll out this build**.

Code changes and embedded-configuration changes are not separate deployment mechanisms. Both produce a new immutable artifact identity and use the same candidate, health-gating, cutover, and rollback flow.

During the demo, multiple terminals keep Grove Shop's **Cluster** TUI view open. They all render the same authoritative control-plane state and update together as nodes join/leave, services move, and rollouts progress.

The browser stays open on the same Grove-managed ingress address and port throughout rollout and rollback. Existing functionality remains reachable, and the new build becomes visible through that same endpoint after cutover.

## What the MVP demo must prove
1. Normal Go application code using Grove's explicit service model.
2. One immutable deployment artifact contains app code, Web UI, runtime/operational surface, metadata, and embedded customer configuration.
3. Multiple real Grovlet processes form a cluster on one host using production-shaped transport.
4. Same-application/same-artifact startup suggests joining the discovered cluster.
5. Same-application/different-artifact startup suggests rollout.
6. Code-only, config-only, and combined changes all use the same artifact rollout path.
7. Services communicate locally and across Grovlets.
8. System NATS carries transient control traffic; JetStream/KV holds authoritative replicated control state.
9. Every open Cluster TUI view reflects the same shared node, placement, health, activity, and rollout state.
10. The application UI is served through a Grove-managed ingress whose externally visible endpoint remains stable across rollout and rollback.
11. The browser continuously polls Grove state and can keep creating/observing orders during mixed-version rollout.
12. A bad embedded configuration can make a candidate component fail; Grove rejects it and restores the previous complete known-good artifact.
13. The business application remains healthy through successful rollout and after rollback.
14. Two ordinary Delve/DAP sessions can debug Orders and Payment workers on different Grovlets without PID, node, or remote-port discovery.

## Minimal operator flow
Build the initial artifact with the good embedded config, then run the same artifact in three terminals:

```bash
./bin/groveshop
./bin/groveshop
./bin/groveshop
```

The first creates the cluster. The next two discover it and suggest joining. Keep the **Cluster** view open in all three terminals.

Keep the browser open on the Web URL exposed by the stable cluster ingress.

For either a code change or a config change, produce a new Grove Shop artifact with the desired embedded config and run it:

```bash
./bin/groveshop-new
```

Because its application identity matches but artifact identity differs, Grove suggests rollout. After confirmation, all existing Cluster views show the same rollout progress live while the browser continues using the same ingress URL.

For the failure proof, build/embed the invalid config into another artifact and run that artifact. The exact same rollout path detects the unhealthy candidate and rolls back the complete artifact.

## Supporting docs
- `ARCHITECTURE.md` — demo services, artifact composition, topology, identity, ingress, and rollout model.
- `UI.md` — browser UI and synchronized Cluster TUI contracts.
- `CONFIGURATION.md` — embedded config as part of the immutable artifact and intentional failure scenario.
- `DEMO_FLOW.md` — exact end-to-end demo sequence and observable states.
- `IMPLEMENTATION_GUIDE.md` — implementation requirements and task mapping.
- `DEBUGGING_DEMO.md` — five-node, two-worker Delve walkthrough.

## Non-goals for MVP v1
- Firecracker live migration.
- Kubernetes integration.
- Edge-specific deployment.
- Sophisticated application behavior.
- Production storefront features.
- Separate deployment semantics for configuration.

The debugging proof does not add DAP multiplexing, cross-service stepping, or general scheduling policy.
