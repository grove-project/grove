Local Development

Status: Draft / evolving

Goal  
A developer should be able to run a complete Grove application locally with minimal external setup.

Expected Experience  
• Build one Grove application artifact.  
• Start the application locally using the Grove runtime.  
• Run multiple services together under the same runtime model used in deployment.  
• Use local process supervision and the same service/version semantics as production.  
• Access local Grove messaging and storage services without assembling an external platform first.  
• Inspect services, replicas, health, placement, and logs through the Grove CLI.  
• Attach Delve through Grove when debugging is enabled.

Principle  
Local mode should preserve the runtime behavior that matters for correctness while collapsing distributed placement onto the developer machine where possible.

What Local Mode Should Avoid  
• Requiring Kubernetes for normal development.  
• Requiring separate hand-built infrastructure stacks for messaging, storage, or service discovery.  
• Introducing development-only service contracts that differ from production.

Open Questions  
• Exact local cluster bootstrap command.  
• Default local persistence behavior.  
• How multi-node behavior is simulated locally.  
• Resource-limit emulation for testing placement and failure behavior.

Natural Go Project Adoption  
Local development should work equally well for a new Grove application and an existing Go repository. grove init should add only the minimum metadata Grove needs and should not restructure the repository.

Suggested developer path:  
• grove init in an existing project, or grove new for a minimal starter.  
• grove dev to build, watch source, refresh any internal bindings/proxies, and run the local Grove runtime.  
• grove test to deploy the same application artifact into an isolated local cluster and execute the developer's existing test command.

Grove should infer sensible Go defaults where possible and require explicit configuration only for non-standard cases.

Local Development Principle  
A developer should feel that they are writing and running a normal Go application. Distributed capabilities should appear progressively as services, persistence, replicas, placement requirements, and production guarantees are added.  
