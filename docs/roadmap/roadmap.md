Roadmap

Status: Draft / directional

Purpose  
Capture Grove’s likely evolution beyond the MVP without treating future ideas as committed implementation plans.

Phase 1 — Runtime Core  
• Single versioned application artifact.  
• Grovlet / worker process model.  
• Multiple services as goroutines inside a worker.  
• Local supervision and health.  
• Embedded NATS foundations.

Phase 2 — Small Distributed Cluster  
• Multi-node membership and cluster state.  
• Desired/observed reconciliation.  
• Placement and replica management.  
• Ingress and service routing.  
• Version-aware discovery and upgrade isolation.

Phase 3 — Durable Production Foundations  
• Replicated persistent application data.  
• Explicit commit/durability semantics.  
• Storage-aware placement and recovery.  
• Failure-domain handling.  
• Stronger security, identity, authorization, and secrets handling.  
• Cluster observability and auditability.

Phase 4 — Developer & Operator Experience  
• Polished Grove CLI.  
• Self-contained end-to-end test harness.  
• Fault injection and topology simulation.  
• Delve-based cluster-aware debugging.  
• Upgrade, rollback, and diagnosis workflows suitable for customer environments.

Phase 5 — Dynamic Optimization  
• Load-aware replica scaling.  
• Communication-aware service placement.  
• Storage locality optimization.  
• Local IPC/direct fast paths where they materially improve performance.  
• Topology-aware routing, including partial-connectivity environments where justified.

Phase 6 — Extensible Runtime Platform  
• Stable extension interfaces for Grove-native infrastructure services.  
• Additional storage/database services.  
• Durable execution / workflow capability.  
• Community-contributed core services where appropriate.

Production-Ready Bar  
Before Grove should be described as production-ready, it needs credible answers for durable HA storage, cluster failure/recovery, security, observability, upgrades/rollback, resource isolation, and operational behavior under partial failure.

Guiding Constraint  
Every roadmap item should be challenged against Grove’s core purpose: make a distributed application easier to develop, test, deploy, understand, operate, and upgrade as one coherent system. Grove should not accumulate platform complexity merely to match feature checklists from general-purpose orchestrators.

Open Planning Questions  
• Exact ordering of storage vs networking hardening.  
• Which security capabilities are mandatory before public trials.  
• Whether Kubernetes-hosted Grove belongs before or after standalone production hardening.  
• When plugin/extensibility APIs should stabilize.  
• What benchmark/demo best proves Grove’s advantage over a conventional microservice deployment.  
