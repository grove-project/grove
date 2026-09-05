ADR-001 — Single Versioned Binary

Status  
Accepted

Context  
Traditional multi-service applications often produce many independently packaged services plus deployment manifests and external infrastructure configuration. This creates a gap between the system developers test locally and the system that is eventually deployed.

Grove aims to make development, end-to-end testing, deployment, troubleshooting, and upgrades operate on the same application artifact.

Decision  
A Grove application is built and distributed as a single versioned binary containing the application services and the Grove runtime components required to execute them.

The same artifact should be usable for local execution and distributed Grove deployment. Runtime placement may still execute application code in worker processes, so “single binary” describes the shipped artifact rather than requiring one operating-system process.

Consequences  
• Deployment and upgrades become artifact-centric.  
• Local end-to-end testing exercises substantially the same code that is shipped.  
• Runtime and application versions have an explicit shared identity.  
• Grove must provide internal lifecycle and placement mechanisms because those responsibilities cannot simply be delegated to independently packaged services.  
• Large applications may produce larger binaries, making build and distribution efficiency important.

Alternatives Considered  
• Independently packaged service binaries or containers — rejected as the default because it recreates deployment composition and local/production drift that Grove is intended to remove.  
• Require Kubernetes packaging — rejected because standalone Grove deployment is a primary use case.

Related Docs  
Grove — System Architecture  
Process Model

Version Alignment Extension  
The single-versioned-binary decision also applies across placement domains. Cloud and customer-edge parts of a Grove application should converge on the same application/runtime binary version. Long-lived version skew is not a compatibility target; it is a temporary lifecycle condition while a release is staged, approved, activated, or rolled back.

This deliberately trades broad cross-version protocol compatibility for stronger upgrade machinery. Grove must make staging, validation, durable cutover, rollback, and customer approval sufficiently safe that keeping edge deployments aligned is practical even when automatic upgrades are constrained.

The value of this decision grows with service count: one coherent application version prevents a growing graph of independently versioned services from becoming an expanding compatibility matrix.  
