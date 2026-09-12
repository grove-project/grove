# Grove Concepts

Grove is a distributed runtime for Go applications. The core idea is to keep the programming model familiar while allowing the same application artifact to grow from a single local process into a distributed cluster spanning cloud and edge environments.

This document defines the main Grove concepts and how they relate to each other.

## The mental model

The most important hierarchy is:

```text
Grove Application
└── Services

Grove Cluster
├── Node
│   └── Grovlet
│       ├── Worker
│       │   ├── Service instance
│       │   └── Service instance
│       └── Worker
│           └── Service instance
└── Node
    └── Grovlet
        └── Worker
            └── Service instance
```

Developers mostly think in **services**.

Grove uses **workers** as execution environments, **Grovlets** as node-local supervisors, and the **cluster** as the global coordination boundary.

Traffic is addressed to services rather than infrastructure:

```text
North-south traffic

External client
      │
      ▼
 Grove Ingress
      │
      ▼
   Service


East-west traffic

   Service
      │
      ▼
  Grove RPC
      │
      ▼
   Service
```

Ingress and Grove RPC are therefore two sides of the same service-addressing model. Neither requires application code to know which worker, Grovlet, node, process, container, or machine currently hosts the destination.

---

## Grove application

A Grove application is a Go application built with the Grove SDK.

It contains the application's services together with the runtime metadata and capabilities Grove needs to operate them.

A Grove application may also contain:

- embedded configuration
- embedded UI or static assets
- service metadata
- durable execution definitions
- placement rules and hints

The built application binary is the primary deployment artifact.

### Role

Keep application code and Grove's runtime model together as one coherent, versioned artifact.

---

## Service

A service is Grove's primary application-level unit.

It represents a logical capability of the application, such as `orders`, `payments`, `inventory`, or `frontend`.

A service is ordinary Go code registered explicitly with Grove. The distributed boundary should remain visible in application code rather than being hidden behind generated proxies or framework magic.

### Role

Define application logic that Grove can place, invoke, observe, restart, upgrade, and recover independently.

### Important property

A service is a **logical component**, not an operating-system process.

A running copy of a service is a **service instance**. Multiple service instances may execute inside the same worker.

---

## Service instance

A service instance is one running copy of a service.

For example, the logical `orders` service may have several service instances distributed across the cluster.

```text
orders
├── instance on Node A
├── instance on Node B
└── instance on Node C
```

### Role

Provide the concrete runtime execution of a logical service while allowing Grove to scale, recover, or move that service without changing its identity.

---

## Worker

A worker is a local execution environment managed by a Grovlet.

It hosts one or more service instances and creates a boundary within which Grove can manage application execution.

Conceptually, a worker may be implemented using different isolation mechanisms over time, including:

- goroutines inside the Grove process
- a subprocess
- a container
- a microVM

The worker abstraction allows Grove to evolve the execution and isolation mechanism without changing the service programming model.

### Role

Provide the execution boundary for service instances.

A worker is the level at which Grove can reason about operations such as starting, stopping, monitoring, debugging, replacing, resource isolation, and eventually migration.

### Developer visibility

Workers are primarily a runtime and operational concept. Application developers should normally think in services rather than workers.

---

## Grovlet

A Grovlet is Grove's node-local supervisor.

Every Grove node runs a Grovlet. The Grovlet owns and manages the workers executing on that node.

Typical responsibilities include:

- starting and stopping workers
- monitoring worker health
- reporting node and execution state
- evaluating local service placement eligibility
- participating in upgrades and recovery
- exposing diagnostics and debugging capabilities
- communicating with the rest of the Grove cluster

### Role

Turn a machine, VM, container, Kubernetes pod, or edge host into a Grove node capable of participating in the cluster.

### Relationship to workers

```text
Node
└── Grovlet
    ├── Worker A
    │   ├── orders
    │   └── payments
    └── Worker B
        └── inventory
```

The Grovlet supervises execution. The workers execute application code.

---

## Node

A node is one compute participant in a Grove cluster.

A node may correspond to a:

- local process environment
- VM
- physical host
- Kubernetes pod
- customer-edge machine

Each node runs a Grovlet and contributes compute capacity to the cluster.

### Role

Provide a physical or virtual place where Grove can run workers and service instances.

---

## Cluster

A Grove cluster is a group of Grove nodes running the same logical application.

A cluster may contain only one node. That means local development can use the same conceptual model as production rather than a separate runtime architecture.

The cluster coordinates capabilities such as:

- service placement
- failure recovery
- service discovery and routing
- cluster state
- upgrades
- ingress
- storage
- observability

### Role

Provide the global coordination boundary for the application.

Grove's goal is that moving from one node to many nodes changes deployment topology, not the application's fundamental programming model.

---

## Control plane

The control plane coordinates cluster-wide desired and observed state.

It tracks concepts such as:

- nodes
- workers
- service instances
- health
- placement decisions
- application version
- configuration version
- upgrade state

### Role

Maintain the shared cluster view needed for coordinated execution and recovery.

Grove uses NATS-based primitives, including NATS KV where appropriate, for distributed coordination.

---

## System NATS

System NATS is Grove's internal communication fabric.

It carries runtime and control traffic such as:

- node heartbeats
- worker lifecycle events
- service health
- placement coordination
- upgrade state
- diagnostics

Application code normally does not interact with System NATS directly.

### Role

Connect Grove's own runtime components and carry cluster-control communication.

---

## Data NATS

Data NATS is the application-facing distributed communication fabric.

It may back capabilities such as:

- service RPC transport
- pub/sub
- application messaging
- object storage

System NATS and Data NATS are logically distinct even if a deployment chooses to host them on the same NATS server.

### Role

Provide distributed data-plane primitives to Grove applications while keeping them separate from Grove's control traffic.

---

## Service placement

Service placement determines which nodes are eligible to execute a service and where its instances should run.

By default, a service with no placement validation can run anywhere in the cluster.

A service may provide explicit placement-validation logic when it has environmental requirements. Each Grovlet evaluates that logic locally.

Examples include:

- reachability to a customer-LAN endpoint
- access to edge-local hardware
- presence of a required device
- locality to another dependency
- a runtime or platform requirement

Only nodes that pass the validation are eligible to host the service.

### Role

Allow application knowledge to influence scheduling without hard-coding customer or infrastructure topology into the cluster scheduler.

This is particularly important for hybrid cloud and edge deployments.

---

## Ingress

Grove Ingress is the cluster capability that routes external, north-south traffic to application services.

Ingress targets a **service**, not a worker, Grovlet, node, IP address, or process.

```text
                         External clients
                                │
                                ▼
                         Grove Ingress
                                │
                      logical service route
                                │
               ┌────────────────┴────────────────┐
               ▼                                 ▼
          Grove Node A                      Grove Node B
          └── Grovlet                       └── Grovlet
              └── Worker A                      └── Worker B
                  └── orders                        └── orders
```

If the `orders` service has instances on several nodes, ingress can route to an appropriate healthy instance without exposing that topology to the caller.

### Role

Provide a built-in path from external clients to logical Grove services.

Ingress belongs conceptually to the **cluster routing layer**, not to a specific worker or Grovlet.

---

## Grove RPC

Grove RPC handles east-west service-to-service communication.

A caller addresses the logical destination service and method. Grove resolves that identity to an appropriate running service instance.

The caller does not need to know the destination's:

- IP address
- hostname
- node
- worker
- process boundary

### Role

Separate service identity from physical placement so services can restart, move, replicate, or execute through different isolation mechanisms without changing application code.

### Relationship to ingress

Ingress and Grove RPC share the same fundamental idea:

```text
Ingress:   external client  → logical service
RPC:       logical service  → logical service
```

The routing mechanism may differ, but the application-facing identity is the service.

---

## Embedded configuration

Grove treats customer or deployment configuration as part of the application artifact rather than as loose files that can drift independently.

The built binary reserves a configuration section. The Grove CLI can compile configuration into Grove's binary representation, compress it, and embed it after the application build.

The CLI can also extract configuration from an existing binary.

If the reserved section is too small, the application must be rebuilt with a larger allocation.

### Role

Make the exact application version and configuration travel together as one inspectable deployment artifact.

A runtime configuration change therefore becomes an explicit new deployment state rather than an uncontrolled mutation of the currently running cluster.

---

## Durable execution

Durable execution is an opt-in Grove programming pattern for operations that must survive failures and retries.

A durable workflow is split into explicit steps. When a step completes successfully, Grove persists its result. If execution later fails and resumes, already-completed steps do not need to execute again.

This is particularly important around side effects such as charging a payment or invoking an external system.

Durable step inputs and results must use Grove-supported serialization, such as gob-compatible values.

### Role

Allow application code to express recoverable workflows while Grove handles persistence, retries, and reuse of completed results.

### Trade-off

Durable execution is not intended for every code path. Persisting and coordinating step state adds overhead and is not appropriate for latency-sensitive operations that do not need those guarantees.

---

## Storage

Grove can expose cluster-aware storage primitives such as:

- key/value storage
- object storage
- durable workflow state

Because Grove also understands service placement and topology, storage can eventually participate in locality decisions, for example placing frequently accessed keys near their producers or consumers.

### Role

Bring application state into the same placement, resilience, and operational model as compute rather than treating storage as an unrelated external concern.

---

## Edge node

An edge node is a Grove node running in a customer or remote environment.

It participates in the same logical cluster as cloud nodes while initiating its connectivity outbound, avoiding a requirement for inbound Internet-accessible ports.

The same Grove concepts should apply at the edge:

- services
- workers
- Grovlet supervision
- placement
- diagnostics
- debugging
- upgrades

### Role

Extend the Grove application into environments where selected services must run close to devices, customer LAN resources, hardware, or data sources.

### Version alignment

Grove should strongly prefer the same application/runtime binary version across cloud and edge nodes. This reduces cross-version compatibility complexity and makes the cluster easier to reason about.

Because edge upgrades can be operationally harder, Grove's upgrade model should be safe, durable, resumable, observable, and easy to roll back.

---

## Upgrades and versions

A Grove deployment is defined by an application version together with its embedded configuration.

During an upgrade, Grove may temporarily run old and new versions side by side while moving execution toward the target state.

Data migrations should support adjacent-version transformations so upgrades and rollbacks can be composed through version chains where possible.

End-to-end tests are version-bound and should be run after migration steps to catch incompatible or lossy transitions.

### Role

Make application evolution an explicit, observable cluster operation rather than a collection of independent process replacements.

---

## Observability and diagnostics

Grove should expose the state of the application in Grove concepts rather than force operators to reconstruct it from infrastructure primitives.

That includes visibility into:

- cluster health
- node health
- workers
- service instances
- placement decisions
- connectivity
- failures and recovery
- configuration
- upgrade progress
- edge status

### Role

Make distributed execution understandable to both developers and operators.

The desired experience is not merely more telemetry; Grove should explain what changed, why it matters, and what the runtime did about it.

---

## Debugging

Debugging is a built-in Grove operational capability.

A developer should be able to target a logical service and let Grove locate the relevant node and worker execution environment.

Grovlets can expose a debugging endpoint and proxy protocols such as DAP/Delve to the appropriate worker.

### Role

Make remote distributed debugging feel as close as practical to debugging an ordinary local Go program.

---

## Grove SDK

The Grove SDK is the developer-facing programming surface.

It should remain small, explicit, and minimally invasive.

Capabilities include concepts such as:

- service registration
- explicit RPC
- placement validation
- durable execution
- storage
- scheduling hints

Grove avoids requiring generated code or framework-style application inheritance. Developers should still be able to import and test their ordinary Go packages directly.

### Role

Expose distributed runtime capabilities without taking ownership of the application's business-code structure.

---

## Grove CLI

The Grove CLI is the primary developer and operator interface to the runtime.

It should expose application concepts rather than forcing users to reason in low-level infrastructure terms.

Typical areas include:

- running the application
- inspecting cluster and service state
- testing
- debugging
- configuration embedding and extraction
- deploying and upgrading
- failure diagnosis and recovery

### Role

Provide one coherent operational surface from local development through production.

---

## Putting it together

A simplified Grove application looks like this:

```text
                               External clients
                                      │
                                      ▼
                               Grove Ingress
                                      │
                                  Service ID
                                      │
             ┌────────────────────────┴────────────────────────┐
             │                                                 │
        Grove Node A                                      Grove Node B
        └── Grovlet                                       └── Grovlet
            ├── Worker A                                      ├── Worker C
            │   ├── frontend                                  │   └── orders
            │   └── orders                                    │
            └── Worker B                                      └── Worker D
                └── payments                                      └── inventory
             │                                                 │
             └────────────── Grove cluster ────────────────────┘
                              │              │
                         System NATS     Data NATS
                              │              │
                        cluster control   app traffic
```

The key responsibilities are:

| Concept | Primary responsibility |
|---|---|
| **Service** | Application logic and logical identity |
| **Service instance** | One running copy of a service |
| **Worker** | Execution environment for service instances |
| **Grovlet** | Node-local supervision and execution management |
| **Node** | Compute participant in the cluster |
| **Cluster** | Global coordination and application runtime boundary |
| **Ingress** | External client → service routing |
| **Grove RPC** | Service → service routing |
| **System NATS** | Grove control traffic |
| **Data NATS** | Application data-plane communication |
| **SDK** | Developer-facing distributed capabilities |
| **CLI** | Developer and operator control surface |

The shortest useful Grove mental model is:

> **Services describe the application. Workers execute them. Grovlets supervise workers. Nodes provide compute. The cluster coordinates everything. Ingress and RPC route by service identity.**

---

## From laptop to cluster

The same concepts should survive as the application grows.

### Local

```text
Application binary
└── Grovlet
    └── Worker
        ├── frontend
        ├── orders
        └── payments
```

### Small cluster

```text
Node A                         Node B
└── Grovlet                    └── Grovlet
    └── Worker                     └── Worker
        ├── frontend                   ├── orders
        └── orders                     └── payments
```

### Cloud and edge

```text
Cloud cluster                  Customer edge
├── frontend                   ├── device-gateway
├── orders                     └── local-collector
└── payments
```

The topology changes. The application model does not.

**Same service identities. Same SDK. Same artifact model. Same operational model.**

---

## Where deeper details live

This document intentionally defines the vocabulary and relationships rather than implementation algorithms.

Detailed mechanisms such as consensus behavior, NATS KV semantics, storage coding, migration internals, upgrade state machines, and debugger transport belong in the architecture and ADR documentation.

Start here to understand **what the pieces are**. Continue into the architecture docs to understand **how Grove implements them**.
