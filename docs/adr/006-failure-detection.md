ADR-006 — Local and Cluster Failure Detection

Status  
Accepted

Context  
A distributed heartbeat alone may take too long to identify a worker that is stuck on its own machine. Conversely, a purely local watchdog cannot determine whether a node is reachable or healthy from the cluster’s perspective.

Decision  
Use two complementary failure-detection tiers.

The Grovlet performs fast local worker supervision using a local heartbeat/watch mechanism and OS process control. It can terminate and restart an unresponsive worker without waiting for distributed consensus.

The cluster control plane uses System NATS and replicated state for distributed health, ownership, placement, and recovery decisions.

Consequences  
• Local failures can be handled with low latency.  
• Cluster communication problems do not prevent a Grovlet from enforcing local worker health.  
• Grove must reconcile local restart actions with distributed desired state.  
• Exact heartbeat transport, timeout, and fencing behavior remain implementation decisions.

Alternatives Considered  
• System NATS heartbeat only — rejected because local enforcement should not depend on distributed messaging latency or availability.  
• Local watchdog only — rejected because cluster-level failure and ownership decisions require distributed observation.

Related Docs  
Grove — System Architecture  
Process Model  
Cluster Control Plane  
