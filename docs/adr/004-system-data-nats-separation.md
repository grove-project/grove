ADR-004 — System and Data NATS Logical Separation

Status  
Accepted

Context  
Grove itself needs highly reliable control-plane messaging and state, while applications may generate high-volume or unpredictable messaging, streams, KV, and object-storage workloads. Treating both as one undifferentiated namespace creates coupling, but requiring two NATS server deployments on every node would add unnecessary overhead.

Decision  
Define System NATS and Data NATS as separate logical planes.

System NATS is reserved for Grove control-plane traffic and cluster state. Data NATS provides application-facing messaging and data capabilities.

The planes may initially share the same NATS server process and be isolated through NATS accounts, permissions, subjects, quotas, and resource policies. Physical separation into different NATS server processes is an operational option rather than an architectural requirement.

Consequences  
• Grove maintains a clean trust and resource boundary between runtime and application traffic.  
• Small deployments can still use one NATS process.  
• Larger deployments can physically separate the planes without changing application architecture.  
• Resource isolation must be strong enough that application load cannot starve the system plane when they share a process.

Alternatives Considered  
• One undifferentiated NATS plane — rejected because application behavior could interfere with control-plane correctness and security.  
• Always run two NATS server processes — rejected because the isolation benefit does not justify the default overhead for small deployments.

Related Docs  
Grove — System Architecture  
Cluster Control Plane  
Storage Architecture  
