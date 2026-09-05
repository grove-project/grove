# Grove — Positioning & Differentiation

Core Positioning

Nomad schedules workloads. Grove schedules application topology.

Grove is an application-aware runtime for distributed Go applications. Developers define the logical application — its services, components, dependencies, data, and expected behavior — without having to permanently encode where every component runs or which isolation boundary separates it from the others.

Grove can choose and evolve the physical execution topology: components may run together as goroutines, as separate processes, inside microVMs, or across cluster nodes. Placement can respond to scale, locality, isolation, resource pressure, resilience, and operational requirements while preserving the application's logical model.

The Key Abstraction Boundary

Traditional orchestrators such as Nomad and Kubernetes generally begin with already-packaged workloads. Their question is: “Where should this workload run?” The internals of the application are mostly opaque to the scheduler.

Grove moves the orchestration boundary into the application. Its question is: “How should this application execute right now?”

For an application topology such as:

API → Parser → Rules Engine → KV

Grove might initially place all four components in one worker process because they communicate heavily and benefit from local access. As load, isolation requirements, or failure domains change, Grove may move the Rules Engine or KV replicas to other processes, microVMs, or nodes without requiring the developer to redesign the logical application.

Why Grove Can Look Like Nomad

From the outside, both systems include familiar cluster machinery: nodes, scheduling, placement, service lifecycle, health monitoring, consensus, networking, and workload recovery. Describing Grove primarily through those mechanisms makes it sound like a lightweight Nomad or Kubernetes alternative.

Those mechanisms are implementation infrastructure, not Grove's defining abstraction. The differentiation appears when describing what the runtime understands and controls: Grove understands the application graph and can optimize its physical execution topology.

Developer Promise

Write the distributed application according to its logical structure. Grove determines how that structure should physically execute.

This allows developers to postpone infrastructure boundaries that normally become architectural commitments. A component does not need to become a network service merely because it might need independent scaling later.

Consequences

This application-aware model enables several Grove capabilities to reinforce one another:

• Locality — tightly coupled components can be co-located and communicate cheaply.  
• Elastic boundaries — execution can move between goroutine, process, microVM, and node boundaries.  
• Storage locality — data placement can be optimized together with compute placement.  
• Resilience — failure domains and replication can be reasoned about using application relationships.  
• Testing — the same logical application can be exercised locally and under distributed/resilience scenarios.  
• Debugging — Grove can provide cluster-aware debugging because it understands both application topology and current placement.  
• Kubernetes interoperability — Grove can run within Kubernetes and interact with conventional workloads without making Kubernetes the application programming model.

Comparison Lens

Nomad: workload orchestrator. Input is jobs/tasks; primary decision is which node should run each allocation.

Kubernetes: container/application infrastructure orchestrator. Input is declarative resources; primary decision is how desired workload state is realized across the cluster.

Grove: application-aware execution runtime. Input is the logical application topology; primary decision is how components should physically execute and be placed.

Positioning Guidance

Avoid leading with “lightweight cluster,” “scheduler,” “single binary,” “Raft,” or “alternative to Kubernetes.” These are important implementation characteristics, but they place Grove immediately into the existing infrastructure-orchestrator category.

Lead with the developer property instead:

“With Grove, you write a distributed application without deciding upfront which parts are local processes, isolated workloads, or remote services. Grove chooses and can evolve the execution topology for you.”

Then explain the cluster, storage, networking, migration, debugging, and resilience mechanisms that make that property possible.

Short Forms

One line: Nomad schedules workloads. Grove schedules application topology.

Developer-oriented: Write the logical application once; let Grove choose its physical execution topology.

Architecture-oriented: Grove separates logical application topology from physical execution topology.

Relationship to Existing Orchestrators

Grove does not necessarily need to replace Nomad or Kubernetes. They can serve as underlying infrastructure environments while Grove operates at the application-aware layer above them. The important boundary is ownership: the infrastructure orchestrator manages machines and workloads; Grove manages how the application's logical components map onto those execution resources.  
