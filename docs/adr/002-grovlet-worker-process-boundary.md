ADR-002 — Grovlet / Worker Process Boundary

Status  
Accepted

Context  
Running all Grove runtime logic and application services in one process would minimize overhead but would also allow an application failure, deadlock, or runaway workload to compromise the node supervisor itself. Running every service in its own process would provide isolation but lose much of Grove’s lightweight co-location advantage.

Decision  
Separate node supervision from application execution using two roles.

The Grovlet is the node-level supervisor and control-plane agent. Application services execute inside a worker process, normally as goroutines. The Grovlet owns worker lifecycle and can terminate or restart an unhealthy worker.

A node should default to the minimum number of workers required. Multiple workers are introduced when isolation, independent lifecycle/versioning, resource separation, or failure containment justifies another process boundary.

Consequences  
• Application failures do not directly take down the node supervisor.  
• Related services can retain low-overhead in-process communication inside a worker.  
• Grove needs a local Grovlet-to-worker supervision and IPC mechanism.  
• Process boundaries become an explicit placement dimension.

Alternatives Considered  
• Everything in one process — rejected because the runtime cannot safely supervise application code from inside the same failure domain.  
• One process per service — rejected as the default because it sacrifices Grove’s lightweight co-location model and increases IPC and operational overhead.

Related Docs  
Grove — System Architecture  
Process Model  
