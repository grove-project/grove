Grove — System Architecture

Status: Concept / evolving design  
Purpose: Canonical high-level architecture for Grove. Detailed behavior belongs in the linked architecture and component documents.

1\. Overview

Grove is a lightweight application runtime and cluster architecture designed around a simple goal: make a multi-service application behave as one self-contained system from local development through production.

A Grove application is shipped as a single versioned binary containing the application services and the Grove runtime components required to run them. The same artifact can run locally, as a standalone distributed Grove cluster, or eventually inside an existing orchestration environment.

The central principle is: what you test locally is what you ship.

2\. Architectural Goals

• Keep deployment and operations substantially simpler than a general-purpose Kubernetes stack.  
• Preserve strong process isolation where failure containment matters while allowing tightly related application services to communicate efficiently.  
• Make local development, end-to-end testing, deployment, troubleshooting, debugging, and upgrades part of the same lifecycle.  
• Optimize placement for locality: related compute and storage should be kept close whenever possible.  
• Provide production-grade cluster coordination, durability, failure detection, and recovery without requiring an external control-plane stack.  
• Keep the runtime extensible so additional infrastructure capabilities can become native Grove services.

3\. Runtime Model

Each machine participating in a Grove cluster is a Grove node. A node runs the Grove binary and contains a node supervisor called the Grovlet.

The Grovlet owns local lifecycle management. It starts and stops workers, observes their health, enforces local decisions quickly, and communicates cluster state through the system control plane.

A worker is the process boundary for application execution. Application services inside a worker may execute as goroutines, allowing services that belong together to share a process and communicate through efficient local paths.

The default design should avoid creating multiple workers without a reason. A single worker can consume the available CPU cores. Additional workers are useful when Grove needs isolation boundaries, independent lifecycle or versioning, resource separation, or failure containment.

4\. Cluster Control Plane

Grove uses an embedded NATS deployment as the system communication and coordination substrate.

System NATS is reserved for Grove itself. It carries cluster control messages and replicated cluster state. NATS JetStream/KV provides a Raft-backed state layer so Grove does not need to introduce a separate etcd-style dependency for the initial architecture.

The replicated system state includes information such as node membership, worker/service desired state, placement, health, ownership, and other control-plane metadata.

The control plane determines cluster leadership from the consensus-backed system state rather than inventing an unrelated leader-election mechanism.

The control plane is responsible for reconciliation: compare desired cluster state with observed state and make placement, lifecycle, recovery, and upgrade decisions.

5\. Local Supervision and Failure Detection

Cluster-level coordination must not be the only mechanism protecting a node.

The Grovlet maintains a fast local health relationship with its worker process. This local heartbeat/watch mechanism is independent of slower distributed failure detection. If a worker becomes unresponsive locally, the Grovlet can terminate and restart it without waiting for a cluster-wide consensus path.

System NATS is used for distributed observation and control, while local OS-level supervision remains the authoritative fast path for detecting a stuck local worker.

This creates two failure-detection tiers:

• Local supervision — fast detection and enforcement by the Grovlet.  
• Cluster supervision — distributed health, ownership, placement, and recovery through the control plane.

6\. Messaging Planes

Grove conceptually separates system traffic from application data traffic.

System NATS carries Grove control-plane traffic and cluster state. Data NATS carries application-facing messaging and data services such as pub/sub, streams, KV, and object storage where those facilities are used by applications.

These are logical planes. They may initially share the same NATS server process and be separated through accounts, permissions, subjects, and resource policies. Grove should only require separate NATS server processes when operational isolation, scaling, durability, or performance requirements justify it.

This keeps the default deployment small without coupling the architecture permanently to a single physical NATS instance.

7\. Networking and Ingress

Ingress is a dedicated Grove runtime component. Any suitable node may expose an ingress endpoint, avoiding dependence on a single special ingress node.

Ingress resolves the target Grove service and routes the request to an appropriate service instance. Routing should prefer the cheapest available path:

• Same worker/process: direct local invocation or the lowest-overhead local transport available.  
• Same node but different process: local IPC or local NATS transport.  
• Remote node: cluster transport, initially using NATS where appropriate.

Because ingress runs outside the worker process, it cannot directly invoke application goroutines. Requests crossing that process boundary require IPC or messaging.

Grove should maintain enough topology information to route between nodes even when the physical network is not a fully connected mesh. Where direct connectivity is unavailable, nodes may relay traffic through reachable peers and prefer the shortest viable path.

8\. Placement and Locality

Placement is not merely a CPU scheduling problem. Grove treats locality as a first-class optimization target.

The control plane should prefer to place services that communicate heavily together in the same worker when safe, then on the same node, and only then across nodes. Storage replicas and data ownership should similarly be biased toward the services that consume the data.

Conceptually, Grove has placement tiers and storage tiers. The control plane can continuously optimize these based on topology, resource availability, communication patterns, durability requirements, and failure domains.

Correctness and required redundancy always take priority over locality optimization.

9\. Storage and Durability

Production Grove requires persistent high-availability storage rather than relying only on local process state.

The storage architecture should support explicit durability semantics: a write can be considered committed only after the required number of durable copies or the configured consistency condition has been satisfied.

Consensus-backed metadata and replicated data are separate concerns. System NATS/Raft maintains authoritative cluster metadata. Application data services may use NATS JetStream/KV/Object Store initially, with the architecture remaining open to specialized Grove-native storage engines later.

The SDK may expose durability or commit semantics to applications where application-level control is valuable, rather than hiding every storage tradeoff behind one fixed policy.

10\. Lifecycle and Upgrades

The single-artifact model is central to Grove lifecycle management. A Grove release packages the runtime and the application services that belong to that version.

Different application versions should not silently communicate merely because their service names match. Cross-version communication is disallowed by default unless explicitly supported by the application/runtime contract.

This gives upgrades a strong compatibility boundary and prevents accidental mixed-version behavior.

The control plane coordinates rollout, placement, health validation, replacement, and rollback while respecting those version boundaries.

11\. Debugging and Operations

Debugging is a runtime capability rather than an external afterthought.

Grove integrates with Delve so debugging can be enabled or attached at runtime when permitted. The Grove CLI should expose basic debugging and troubleshooting operations with knowledge of the actual cluster topology, service placement, worker state, and version.

This enables the same operational model during local development, end-to-end testing, customer deployments, and production troubleshooting.

12\. Extensibility

Grove should make runtime infrastructure services pluggable. The core provides lifecycle, placement, communication, storage primitives, cluster state, and observability. Additional capabilities can build on those primitives.

For example, a durable-execution engine comparable in purpose to Temporal could eventually be implemented as a Grove-native runtime service without requiring every application deployment to assemble another external infrastructure stack.

13\. Architecture Boundaries

The initial architecture intentionally separates several concepts:

• Grovlet vs worker — node supervision is separate from application execution.  
• System plane vs data plane — Grove coordination is separate from application traffic.  
• Consensus metadata vs application data — cluster correctness does not imply one universal storage engine.  
• Local failure detection vs distributed failure detection — local enforcement must remain fast even during cluster communication problems.  
• Logical isolation vs physical processes — separate planes do not automatically require separate server processes.

14\. Current Component Map

Grove binary  
  ├─ Grovlet  
  │   ├─ local worker supervision  
  │   ├─ local heartbeat / watchdog  
  │   └─ node-side control-plane agent  
  │  
  ├─ Worker  
  │   └─ application services as goroutines  
  │  
  ├─ Ingress  
  │   └─ service resolution and request routing  
  │  
  ├─ System NATS  
  │   ├─ control messaging  
  │   ├─ cluster state  
  │   └─ Raft-backed KV / consensus  
  │  
  ├─ Data NATS  
  │   ├─ application messaging  
  │   ├─ streams / KV  
  │   └─ object storage  
  │  
  └─ CLI / Debugger integration

15\. Open Architecture Questions

The following areas are intentionally not considered final yet:

• Exact local Grovlet-to-worker heartbeat mechanism and timeout policy.  
• Exact local IPC transport between ingress/runtime components and workers.  
• How much application traffic should use NATS versus specialized direct transports.  
• Precise cluster-leader responsibilities versus distributed reconciliation responsibilities.  
• Storage replication protocol and consistency levels beyond the initial NATS-backed implementation.  
• Placement scoring and dynamic locality optimization algorithms.  
• Stable ingress endpoint discovery in environments without an external load balancer.  
• Authentication, authorization, workload identity, secrets, and transport security model.  
• Upgrade state machine and rollback semantics.  
• Observability architecture.

16\. Documentation Model

This document is the canonical system-level view. Detailed decisions should be expanded in the Process Model, Cluster Control Plane, Networking & Ingress, and Storage Architecture documents. Decisions with meaningful alternatives or long-term consequences should also receive an Architecture Decision Record (ADR).

17\. Kubernetes Bridge and Incremental Migration  
Grove can run inside an existing Kubernetes cluster rather than requiring Kubernetes to be replaced before Grove can be adopted. A Grovlet may run as a Kubernetes Pod and use the Kubernetes API as an infrastructure adapter.

In this mode, Kubernetes is a possible infrastructure substrate for Grove. The Kubernetes Bridge allows the Grovlet to provision additional Grovlet Pods/nodes, discover existing Pods, Services, and endpoints, observe Kubernetes health and topology, and translate relevant Kubernetes resources into Grove's service and topology model.

Existing Kubernetes workloads can be represented to Grove as bridged or external services. Grove-native services may therefore communicate with legacy Kubernetes applications through the normal Grove service-routing model while the legacy workload continues to run unchanged under Kubernetes.

This enables incremental migration rather than a big-bang rewrite:  
• Legacy Kubernetes application — ordinary Kubernetes workload discovered through the bridge.  
• Grove-managed Kubernetes application — a service moves into a Grove worker while the Grovlet itself still runs on Kubernetes-provided compute.  
• Grove-native application — the application is fully managed by Grove and can eventually run independently of Kubernetes if desired.

A deployment may contain all three states simultaneously. For example, one service may run inside a Grove worker while its upstream service and database remain ordinary Kubernetes workloads. Grove routes across the boundary and maintains a unified topology view.

Concrete migration example:  
frontend → users-service → orders-service → postgres

Initially, all four components are ordinary Kubernetes workloads. After Grove is installed in the cluster, the Grovlet discovers their Services, Pods, endpoints, and relationships and represents them in the Grove topology.

The first migration step can move only orders-service into a Grove worker. frontend and users-service remain ordinary Kubernetes applications, and postgres remains Kubernetes-managed infrastructure. Requests from users-service to orders-service cross the Kubernetes Bridge and are routed into the Grove worker without requiring the rest of the application to migrate.

A later step can move users-service into Grove as well. Communication between users-service and orders-service can then use Grove-native routing and benefit from locality-aware placement, while frontend and postgres remain Kubernetes workloads.

Eventually frontend may also migrate. At that point Kubernetes may simply provide the Pods or machines on which Grovlets run, or the Grove deployment can move away from Kubernetes entirely. At every intermediate stage the application remains functional and Grove maintains one logical topology spanning both environments.

The bridge should also allow Grove operational capabilities to span the migration boundary where practical. Topology inspection, health views, tracing, end-to-end and resilience testing, and troubleshooting should show Grove-native and Kubernetes-hosted legacy components as one application graph instead of creating disconnected operational worlds.

The architectural boundary is important: Kubernetes remains responsible for the resources it owns, while Grove owns Grove application semantics, worker lifecycle, service topology, routing, testing, debugging, and Grove cluster state. The bridge adapts between those models rather than duplicating the entire Kubernetes control plane.

This provides a deliberate adoption path: install Grove into an existing Kubernetes cluster, integrate existing workloads, migrate services incrementally, and remove the Kubernetes dependency only when and if it no longer provides value.

18\. Native Applications and WASM Extensions

Grove deliberately separates the primary application execution model from the extension model.

Native Go is the application model. Grove services are ordinary Go code running with the normal Go runtime and operating-system capabilities. They retain goroutines, native libraries, standard networking, filesystem and OS integration, pprof, Delve debugging, and direct IDE navigation. Grove takes responsibility for distribution, placement, process or microVM isolation, routing, lifecycle, and state migration where feasible rather than requiring application code to target a restricted execution environment.

WASM is the extension model. Small plugins may be implemented in any language that can target WebAssembly/WASI, such as Rust, Go/TinyGo, C/C++, or other suitable languages. Grove loads these plugins into a sandboxed runtime and exposes a deliberately constrained host capability API.

Typical WASM plugin use cases include placement policies, routing policies, authorization hooks, ingress middleware, event/data transformations, custom scheduling logic, and other bounded extension points. Plugins should be small and replaceable rather than becoming the primary container for application services.

The Grove host controls which capabilities a plugin receives. Examples may include logging, KV access, events, HTTP, secrets, topology queries, or explicitly permitted Grove service calls. A plugin does not receive unrestricted operating-system access merely because the host has it. This makes capability boundaries explicit and gives Grove a language-neutral extension mechanism without embedding arbitrary language runtimes or trusting arbitrary native binaries.

The architectural rule is:

• Native Go services \= trusted, powerful application code.  
• WASM plugins \= portable, sandboxed extension code.

This distinction intentionally uses WebAssembly's restrictions where they are beneficial. For primary applications, those restrictions would reduce compatibility with normal Go programs, native libraries, Linux facilities, networking, and debugging. For plugins, the same restrictions provide isolation, portability, controlled capabilities, and cheap dynamic loading.

WASM therefore complements rather than replaces Grove's native execution model. A future Grove implementation may support WASM as an optional workload type, but Grove-native application development should not require developers to compile their services to WASM.

19\. Edge-Aware Cluster Topology  
Grove treats customer edge environments as first-class placement domains inside the same logical application cluster, not as a separate agent architecture. Services may be constrained or preferred to run in cloud, at a specific customer edge, or close to related services and data.

An edge Grovlet initiates an outbound connection back to the cloud-side Grove fabric/control plane. Customer networks do not need to expose inbound Internet ports for Grove control, diagnostics, service communication, or debugging. The established outbound path carries the cluster relationship back to the rest of Grove while preserving the customer network boundary.

Edge connectivity may be intermittent. Grove should distinguish cloud-disconnected from edge-unhealthy: edge-local services can remain healthy and, where their application contract permits, continue autonomous work, queue state/events, and reconcile after connectivity returns.

The operational model must remain uniform across cloud and edge. Cluster status should expose edge connectivity, latency, last heartbeat, runtime/application version, service health, queued work, storage state, and autonomous/disconnected mode. Logs, traces, inspection, and DAP/Delve debugging should route through the existing Grove connection so operators do not need SSH, public debugger ports, or customer firewall changes.

20\. Version Alignment Across Cloud and Edge  
Grove should actively converge all cloud and edge placement domains on the same application/runtime binary version. Version skew is a temporary upgrade condition, not the intended steady state. This keeps the distributed application a coherent compatibility unit and avoids making every internal service protocol support long-lived cross-version compatibility.

Customer edge upgrades may require approval or maintenance windows, so Grove should separate staging from activation. A candidate release can be distributed, verified, booted side-by-side, and validated while the current version remains authoritative. Running two versions is allowed for validation and rollback readiness, but ordinary production traffic is owned by one coherent application version at a time; Grove does not depend on canary traffic splitting between application versions.

The desired lifecycle is prove, approve, cut over, retain rollback, then garbage-collect. Grove validates the candidate, presents evidence to the customer/operator, performs a coordinated cutover after approval, retains the previous release and recovery metadata for a rollback window, and removes them only after explicit commit/retirement policy permits it. Upgrade progress itself must be durable and resumable across process, machine, or power failure.

21\. Versioned State Migration and Self-Validating Releases  
A Grove release is a self-validating migratable artifact. In addition to application/runtime code, each release carries its version-specific E2E contract and, when persisted representation changes, the adjacent two-way migration adapter between the previous version and itself.

Developers are required to implement only N ↔ N+1 state migration boundaries. Grove composes these adjacent adapters into a chain, allowing state to move between arbitrary retained versions without requiring every version to understand every historical schema. A downgrade may be lossy; Grove must report known/described loss, but semantic validity is determined by whether the target version can correctly operate on the migrated state.

Migration is executed and validated hop-by-hop. For each transition Grove migrates state to the adjacent target version, boots that target binary, runs the E2E suite compiled into that target release, validates its declared correctness/SLA contract, checkpoints success, and only then proceeds to the next hop. The same process applies in reverse during rollback.

The target version's E2E suite is authoritative for that hop because correctness evolves with the application. A newer suite must not be used to judge an older version for functionality that did not yet exist. A migration that returns success is therefore not sufficient; the target release must prove that its migrated state is operationally valid.

This makes the release conceptually: binary \+ adjacent N-1 ↔ N migration \+ version-specific E2E contract \+ version-specific SLA expectations.

22\. Complexity Scaling Principle  
Grove's value increases as the application graph grows. Without a coherent application lifecycle, more services multiply the combinations of placement, versions, routing, migrations, failure modes, edge/cloud boundaries, and rollback paths that operators must coordinate. Grove deliberately collapses this growing graph back into one manageable application lifecycle: deploy version N, validate version N, cut over to version N, or migrate/rollback to another coherent application version.

The architectural objective is not merely to make individual services easier to run. It is to keep a large distributed application understandable and operable as one application as its component count and topology complexity increase.  
