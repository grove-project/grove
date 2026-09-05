MVP

Status: Draft / planning

Goal  
Validate Grove’s core thesis with the smallest implementation that proves a self-contained multi-service application can move from local development to a small distributed deployment with the same runtime model.

MVP Must Prove  
• One Grove binary can package multiple Go services plus the runtime.  
• A Grovlet can start, supervise, and restart a worker process.  
• Services inside a worker can run as goroutines.  
• Embedded System NATS can carry control messages and replicated control-plane state.  
• A small multi-node cluster can discover members and maintain desired/observed state.  
• Ingress can resolve and route to services.  
• Version isolation prevents accidental old/new service communication.  
• The complete application can run locally for realistic end-to-end tests.  
• Grove CLI can inspect nodes, workers, services, health, and version.  
• Delve can be attached through a Grove-aware debugging flow.

Storage Scope  
The MVP should include enough durable replicated state to demonstrate HA semantics for a basic KV/object use case. Production-grade storage remains a milestone rather than something to over-design before the runtime model is validated.

Suggested MVP Sequence  
1. Single-node process model: Grovlet + worker + multiple services.  
2. Embedded NATS and local control messages.  
3. Two/three-node membership and replicated control state.  
4. Ingress and service routing.  
5. Local heartbeat, worker kill/restart, and reconciliation.  
6. Version-isolated deployment/update flow.  
7. Basic replicated data service and commit semantics.  
8. CLI inspection and Delve integration.  
9. End-to-end demo covering develop → test → deploy → fail → debug → upgrade.

Explicitly Out of MVP  
• Full Kubernetes replacement.  
• Arbitrary third-party workloads.  
• Sophisticated multi-hop routing optimization.  
• General-purpose scheduler policy language.  
• Mature plugin ecosystem.  
• Full production security/observability suite.

Exit Criteria  
The MVP is successful when the same application artifact can be run locally, deployed across a few machines, survive a worker/node failure in a demonstrable way, be inspected/debugged with Grove tooling, and upgraded without mixed-version service communication.  
