Developer Experience Principles

Status: Draft / evolving

Goal  
Make Grove intuitive across a wide range of experience levels without hiding the distributed system from developers who need to inspect it.

Core Principles  
• Progressive disclosure: beginners can run and test applications without learning every distributed-system primitive; experienced developers can drill into placement, routing, storage, replicas, workers, and cluster state.  
• Application-first mental model: the default unit of interaction is the application and its services, not infrastructure objects.  
• Hide mechanisms, never hide state: Grove may automate placement, routing, failover, leader election, and replication, but developers must always be able to ask where something runs, why it is there, what happened, who it talks to, where its data lives, and what happens on failure.  
• Same model everywhere: local development, testing, staging, and production should use the same application artifact, service contracts, and runtime semantics.  
• Intent over infrastructure: developers state desired behavior and guarantees; Grove decides how to realize them.  
• Native Go first: business code should look like ordinary Go and should remain usable outside Grove where practical.  
• Minimal repository opinion: Grove defines contracts and runtime semantics, not folder layout or package organization.

Progressive CLI Experience  
The normal path should stay compact: run, test, deploy, inspect, debug. More detailed commands should reveal deeper runtime state only when requested.

Key UX Statement  
Grove should make distributed applications feel like ordinary applications until the developer needs them to feel distributed.

Migratability as a Developer Experience Principle  
Grove should encourage applications to be migratable by default. Mobility should feel like a normal runtime capability rather than a specialized infrastructure feature.

• Prefer migratable primitives: Grove networking, storage, messaging, clocks, leases, and service clients should be the easiest APIs to reach for.  
• Make escape hatches explicit: applications may use raw Linux facilities, host-local files, raw sockets, physical devices, or host IPC, but Grove should clearly surface the mobility capability lost by doing so.  
• Preserve ordinary Go: migratability must not require rewriting business logic around Grove. The SDK should concentrate portability at composition and capability boundaries.  
• Declare mobility intent: a service may claim a mobility class such as Seamless, Reconnect, or Pinned. This is a contract that Grove can validate rather than a scheduler hint only.  
• Test the contract: grove test should be able to freeze a running service, relocate it to another node, resume it with execution state intact, and continue the active E2E flow while checking correctness and SLA behavior.  
• Explain violations: Grove tooling should report exactly which acquired resource or API use prevents transparent migration.

Mobility Classes  
Seamless — Grove expects the service to survive suspension and relocation without observable application failure.  
Reconnect — execution state may move, but one or more external resources must reconnect or rebind after resume.  
Pinned — the service currently depends on a node-local or physical resource that Grove cannot relocate transparently.

Key UX Statement  
Developers should not have to become checkpoint/restore experts. Grove should make the portable path natural, continuously tell them when they leave it, and prove migratability through tests.  
