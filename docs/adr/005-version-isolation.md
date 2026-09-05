ADR-005 — Version Isolation by Default

Status  
Accepted

Context  
During rolling upgrades, different versions of a Grove application may temporarily exist in the same cluster. Allowing services from different versions to discover and communicate with each other automatically can create combinations that were never tested and require every internal protocol change to remain backward compatible.

Decision  
Grove service discovery and routing are version-scoped by default. A service instance from a new application version does not communicate with instances belonging to an old version merely because they share the same logical service name.

Cross-version communication requires an explicit application/runtime contract rather than being implicit.

Consequences  
• A Grove release behaves as a coherent compatibility unit.  
• What was tested together remains what communicates together during deployment.  
• Rolling upgrades require routing and ingress logic that understands version ownership.  
• Stateful migrations and intentional cross-version protocols need explicit mechanisms.  
• Grove can avoid imposing universal backward compatibility on every internal service interface.

Alternatives Considered  
• Kubernetes-style service discovery across mixed versions by default — rejected because it permits untested version combinations.  
• Require every internal service protocol to be backward compatible — rejected as an unnecessary burden for the default Grove application model.

Related Docs  
Grove — System Architecture  
Process Model

Upgrade Clarification  
Grove does not use ordinary canary traffic splitting between application versions as the default upgrade mechanism. Two versions may run side-by-side so the candidate can boot, validate, and remain ready for cutover/rollback, but production ownership remains with one coherent application version at a time. This prevents cross-version service graphs, duplicate side effects, and ambiguous state ownership.

The preferred transition is candidate staging and E2E validation followed by a coordinated cutover. The previous version remains rollback-ready for a bounded window. Persisted-state compatibility across that transition is handled by explicit adjacent N ↔ N+1 migration adapters rather than by requiring all application versions to understand each other's schemas indefinitely.  
