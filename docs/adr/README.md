Grove — Architecture Decision Records

Purpose  
This document is the decision register for Grove. Architecture documents describe the current system; ADRs preserve why consequential architectural choices were made. Accepted ADRs remain part of the historical record even if a later ADR supersedes them.

Status vocabulary  
Proposed — under discussion.  
Accepted — current architectural decision.  
Superseded — replaced by a later ADR.  
Rejected — considered but not adopted.

Decision Register

ADR-001 — Single Versioned Binary — Accepted  
One versioned artifact contains the Grove runtime and application services. Runtime process boundaries do not change the single-artifact distribution model.

ADR-002 — Grovlet / Worker Process Boundary — Accepted  
Node supervision runs outside the application worker failure domain while related application services can execute as goroutines inside a worker.

ADR-003 — Embedded NATS for Control Plane — Accepted for initial architecture  
System NATS plus JetStream/KV provides control messaging and Raft-backed replicated state without requiring a separate etcd-style subsystem.

ADR-004 — System and Data NATS Logical Separation — Accepted  
Control-plane and application traffic are separate logical planes but may share one NATS server process until physical isolation is justified.

ADR-005 — Version Isolation by Default — Accepted  
Service discovery and routing are version-scoped by default so old and new application versions do not accidentally communicate during upgrades.

ADR-006 — Local and Cluster Failure Detection — Accepted  
Fast Grovlet-to-worker supervision handles local failure while the distributed control plane handles cluster health, ownership, placement, and recovery.

ADR-007 — Locality-Aware Placement — Accepted as architectural principle  
Grove optimizes related compute and data toward the same worker or node when correctness, redundancy, and capacity permit.

ADR-008 — Ingress on Any Node — Accepted as architectural principle  
Any suitable Grove node may expose ingress and route toward the cheapest valid version-compatible service instance.

ADR-009 — DAP as Grove Debugging Interface — Accepted  
DAP is the IDE-facing debugging protocol. The Grovlet resolves service/process placement and initially tunnels a standard Delve DAP session; multi-process DAP multiplexing may be added later.

ADR format  
Each ADR should contain: Status, Context, Decision, Consequences, Alternatives Considered, and Related Docs.

ADR-010 — Adjacent Reversible Migrations and Version-Bound E2E — Accepted  
Each release provides only its adjacent N-1 ↔ N persisted-state adapter and its own compiled E2E correctness contract. Grove composes migration chains across versions and validates every hop by booting and testing the target release, including downgrade paths that may be explicitly lossy.  
