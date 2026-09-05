Debugging

Status: Draft / evolving

Goal  
Make debugging a Grove-native operational capability rather than a separate deployment-specific workflow.

Core Direction  
Grove should integrate with Delve so debugging can be enabled or attached at runtime when permitted. The Grove CLI should help identify the correct service, version, worker, replica, and node before attaching.

Expected Workflow  
• Inspect current cluster and service state.  
• Select a service or specific replica.  
• Identify its worker and node.  
• Enable or attach a Delve session through Grove.  
• Debug using cluster-aware context rather than manually locating a process.  
• Stop the debugging session and return the worker to normal operation.

Production Debugging  
Remote debugging can be extremely valuable for support and customer environments, but it is privileged. Grove must require explicit authorization and should make every production debugging action auditable.

Desired CLI Directions  
• grove ps / status-style inspection.  
• grove debug <service> or equivalent target selection.  
• Runtime start/stop of debugging where technically possible.  
• Capture surrounding logs, health, version, placement, and recent failures.

Open Questions  
• Authentication and authorization model.  
• Delve transport and exposure model.  
• How pausing a goroutine/process affects health checks and failover.  
• Safe behavior for replicated services while one replica is being debugged.

Application-First Troubleshooting  
Grove should present failures in application vocabulary before infrastructure vocabulary. A developer should be able to start from a service or business flow and progressively reveal workers, nodes, routing, storage, and control-plane details.

A command such as grove why <service> should correlate recent restarts, dependency failures, placement changes, routing events, storage transitions, logs, and traces into a concise explanation with suggested next inspection steps.

Hide Mechanisms, Never State  
Automation should not make the runtime opaque. Developers must be able to answer where a service is running, why Grove placed it there, which dependencies it is using, where its data is located, and what changed around a failure.

Debugging UX Principle  
The developer should not need to manually discover a node, SSH into it, find a PID, forward a port, and then attach Delve. Grove already knows the service, version, worker, node, and surrounding runtime state and should use that context to make debugging direct.

Edge Debugging and Diagnostics  
Edge nodes must provide the same Grove troubleshooting and debugging model as cloud nodes. An edge deployment is not a separate operational product: Grove should expose its topology, service state, version, logs, traces, storage health, connectivity, and debugger targets through the same cluster-aware tools.

Customer edge networks should not need to expose SSH, Delve/DAP, Grove, or other inbound Internet ports. The edge Grovlet initiates the trusted outbound connection to the cloud-side Grove fabric, and diagnostics/debugging sessions are routed back through that existing relationship subject to authorization and audit policy.

Edge status should make network-boundary problems obvious. Operators should be able to distinguish a healthy autonomous edge whose cloud connection is temporarily unavailable from a failed edge runtime. Useful status includes connection state and duration, latency, last heartbeat, current/desired version, local service health, queued work, storage health, and autonomous/disconnected mode.

The debugging UX principle therefore applies regardless of placement: the operator selects the application/service; Grove resolves whether it is local, cloud, or customer edge and routes the diagnostic or DAP session to the correct process without requiring manual network access.  
