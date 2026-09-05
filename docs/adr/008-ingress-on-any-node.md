ADR-008 — Ingress on Any Node

Status  
Accepted as an architectural principle

Context  
Requiring one dedicated ingress node creates a special cluster role and a potential availability bottleneck. Grove nodes already participate in service discovery and cluster routing, so ingress can be distributed across suitable nodes.

Decision  
Any suitable Grove node may expose ingress. Ingress resolves a target Grove service and routes the request to an appropriate version-compatible service instance.

Routing should prefer the cheapest valid path: same worker/process, same node across a process boundary, direct remote node, and eventually relay through reachable peers when the topology is not fully connected.

Ingress remains a dedicated runtime component rather than application code inside the worker. Therefore requests entering a worker cross an IPC or messaging boundary.

Consequences  
• Grove does not depend on a unique ingress node.  
• Multiple nodes can provide external entry points for availability and scale.  
• Service routing can exploit Grove’s locality knowledge.  
• Grove still needs a mechanism for clients or external infrastructure to discover stable ingress endpoints.  
• The exact IPC and remote transport are separate implementation decisions.

Alternatives Considered  
• Dedicated ingress node — rejected because it creates an unnecessary special role and failure concentration.  
• Run ingress inside each worker — rejected because ingress is cluster/runtime infrastructure and needs routing visibility beyond one worker.

Related Docs  
Grove — System Architecture  
Networking & Ingress  
