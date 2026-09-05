Comparisons, Prior Art & Experiments

Status: Living research index

Purpose  
Track systems, papers, and experiments that inform Grove without turning research notes into architecture decisions prematurely.

Research Areas  
• Lightweight schedulers and orchestrators: Nomad, k3s, Kubernetes, systemd-style supervision.  
• Embedded/distributed messaging: NATS, JetStream, request/reply, KV, Object Store.  
• Consensus and replicated state: Raft, etcd-style control planes, read consistency.  
• Process isolation and lightweight virtualization: containers, KVM, microVMs, Firecracker.  
• Service routing and ingress: local IPC, NATS routing, topology-aware routing, partial-connectivity meshes.  
• Durable storage: replication, quorum/commit semantics, locality-aware data placement.  
• Debugging and operations: Delve, cluster-aware debugger attachment, production troubleshooting.  
• Durable execution and extensibility: Temporal-like runtime services and plugin models.

How to Use This Folder  
Research findings should record what was evaluated, what Grove can reuse, what differs from Grove’s goals, and which open architecture question the research informs.

When a research conclusion becomes a deliberate architectural choice, capture it in an ADR rather than leaving the decision only in research notes.

Experiment Backlog  
• Measure same-process goroutine calls vs local IPC vs NATS request/reply.  
• Stress System/Data NATS logical isolation under application load.  
• Prototype Grovlet-to-worker local heartbeat and kill/restart behavior under CPU starvation.  
• Simulate NATS-backed control-plane leader loss and recovery.  
• Evaluate storage commit semantics across 1/2/3 replicas.  
• Measure locality-aware placement benefits for chatty services.  
• Prototype version-isolated rolling upgrade and ingress switchover.

Open Research Questions  
This document is intentionally exploratory. Results here are evidence, not accepted architecture, until reflected in an ADR or canonical architecture document.  
