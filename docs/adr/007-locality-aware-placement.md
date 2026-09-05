ADR-007 — Locality-Aware Placement

Status  
Accepted as an architectural principle

Context  
General-purpose schedulers commonly focus on fitting workloads onto available resources. Grove can exploit additional knowledge: services are part of one application artifact, their communication relationships are known or observable, and Grove may control both compute placement and data placement.

Decision  
Treat locality as a first-class placement objective.

When correctness, durability, resource requirements, and failure-domain constraints permit, Grove should prefer placement in this order: related services in the same worker, then on the same node, then across nodes. Data and storage replicas should similarly be biased toward their consumers when doing so does not violate durability requirements.

The control plane may dynamically re-optimize placement as topology, load, communication patterns, and storage access patterns change.

Consequences  
• Common service-to-service paths can avoid network and serialization overhead.  
• Storage access can become more local.  
• Placement becomes a multidimensional optimization problem rather than simple bin packing.  
• Grove needs metrics and scoring capable of estimating communication and storage locality benefits.  
• Redundancy and correctness always override locality.

Alternatives Considered  
• Resource-only scheduling — rejected because it ignores one of the strongest optimization opportunities created by Grove’s integrated runtime.  
• Force all related services onto one node — rejected because availability, capacity, and durability constraints sometimes require distribution.

Related Docs  
Grove — System Architecture  
Storage Architecture  
