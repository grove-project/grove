ADR-003 — Embedded NATS for Control Plane

Status  
Accepted for the initial architecture

Context  
Grove requires distributed control messaging, replicated cluster state, consensus, membership-related coordination, and leader-backed decisions. Introducing separate systems for messaging and consensus would increase deployment complexity and conflict with Grove’s goal of a compact self-contained runtime.

Decision  
Embed NATS as Grove’s system communication substrate. Use JetStream/KV as the initial replicated cluster-state layer, relying on its Raft implementation rather than introducing a separate etcd-style consensus dependency.

System NATS carries Grove control messages and authoritative replicated control-plane state. The Grove control plane builds reconciliation and cluster semantics on top of those primitives.

Consequences  
• Grove gets messaging and consensus-backed state from one embedded infrastructure technology.  
• The standalone deployment remains small and self-contained.  
• Grove becomes operationally dependent on NATS/JetStream correctness and availability for control-plane functions.  
• Grove must define carefully which state belongs in NATS KV and which behavior belongs in Grove’s own reconciliation logic.  
• The architecture can later replace or augment individual storage mechanisms without changing the high-level control-plane contract.

Alternatives Considered  
• Implement a custom Raft-backed KV — rejected initially because NATS already provides the required consensus machinery.  
• Embed etcd plus a separate messaging system — rejected because it adds another distributed subsystem and operational surface.  
• External NATS only — rejected as the default because Grove should be capable of self-contained deployment.

Related Docs  
Grove — System Architecture  
Cluster Control Plane  
