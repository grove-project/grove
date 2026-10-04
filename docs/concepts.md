# Grove Concepts

Grove is a distributed runtime for Go applications. The core idea is to keep the programming model familiar while allowing the same application artifact to grow from a single local process into a distributed cluster spanning cloud and edge environments.

This document defines the main Grove concepts and how they relate to each other. The [glossary](glossary.md) gives each term's exact definition and its name in code.

## The mental model

The most important hierarchy is:

```text
Grove Application
└── Services (declared as components)
    └── Handlers

Grove Cluster
├── Node
│   └── Grovlet
│       ├── Application runtime
│       │   ├── Service instance
│       │   └── Service instance
│       └── Worker (isolated service)
│           └── Service instance
└── Node
    └── Grovlet
        └── Application runtime
            └── Service instance
```

Developers mostly think in **services** and their **handlers**.

Grove runs services in **application processes**: by default one shared **application runtime** per node, plus a dedicated **worker** process for any service isolated explicitly. **Grovlets** supervise those processes on each node, and the **cluster** is the global coordination boundary.

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

Ingress and Grove RPC are therefore two sides of the same service-addressing model. Neither requires application code to know which process, Grovlet, node, container, or machine currently hosts the destination.

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

A service is a logical unit, not an operating-system process.

An application declares each service to the runtime as a **component** (`runtime.Component`): its service ID, name, registration hook, and optionally an HTTP handler for ingress and a list of declared handlers. "Component" is the declaration, "service" is what it provides.

A running copy of a service is a **service instance**. Many service instances share one application runtime process.

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

## Handler

A handler is one method of a service: a function from a request to a response, registered under the service's ID and a method ID. Calls address a service and a method.

A component may **declare** its handlers. Grove then places each declared handler on its own:

- an **automatic** handler runs on every node hosting its component, and calls are spread across them
- an **exclusive** handler has exactly one active owner in the cluster, which holds a lease on the handler's **capability** and must stop acting when it loses it

### Role

Let Grove scale and fence individual operations, not only whole services.

---

## Application runtime and workers

Application code runs in **application processes** that the Grovlet starts and supervises. The Grovlet itself never runs application code.

- The **application runtime** is the one process per node that runs every hosted service by default, as goroutines sharing the process.
- A **worker** is a dedicated process for one service that was isolated explicitly, for example to contain its failures or debug it on its own.

Both are execution choices, not part of the service's shape. Calls between services in the same process skip the network; all other calls go through Grove RPC. See [the process model](architecture/process-model.md).

### Role

Provide the execution and failure boundary for service instances. A process is the level at which Grove starts, stops, monitors, debugs, and replaces running code. Container or microVM isolation could later be added as other kinds of worker.

### Developer visibility

Application processes are a runtime and operational concept. Application developers normally think in services and handlers.

---

## Grovlet

A Grovlet is Grove's node-local supervisor.

Every Grove node runs a Grovlet. The Grovlet owns and manages the application processes executing on that node.

Typical responsibilities include:

- running the node's System NATS and control-plane participation
- starting and stopping the application runtime and workers
- monitoring process and service health
- reporting node and execution state
- evaluating local service placement eligibility
- participating in upgrades and recovery
- exposing diagnostics and debugging capabilities
- communicating with the rest of the Grove cluster

### Role

Turn a machine, VM, container, Kubernetes pod, or edge host into a Grove node capable of participating in the cluster.

### Relationship to application processes

```text
Node
└── Grovlet
    ├── Application runtime
    │   ├── orders
    │   └── inventory
    └── Worker (isolated)
        └── payments
```

The Grovlet supervises execution. Application processes execute application code.

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

Provide a physical or virtual place where Grove can run application processes and service instances.

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
- application processes
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
- component lifecycle events
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

## Placement

Placement determines which nodes run each service and handler. It works at two levels: **handler placement**, the primary one, decides which nodes serve each declared handler; **service placement** decides which single node owns a service's endpoint, which the ingress and undeclared handlers use. See [placement](architecture/placement.md).

Today every node hosts every component, so a service can run anywhere in the cluster.

Planned: a service may provide explicit placement-validation logic when it has environmental requirements. Each Grovlet would evaluate that logic locally.

Examples include:

- reachability to a customer-LAN endpoint
- access to edge-local hardware
- presence of a required device
- locality to another dependency
- a runtime or platform requirement

Only nodes that pass the validation would be eligible to host the service.

### Role

Allow application knowledge to influence scheduling without hard-coding customer or infrastructure topology into the cluster scheduler.

This is particularly important for hybrid cloud and edge deployments.

---

## Ingress

Grove Ingress is the cluster capability that routes external, north-south traffic to application services.

Ingress targets a **service**, not a Grovlet, node, IP address, or process.

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
              └── App runtime                   └── App runtime
                  └── orders                        └── orders
```

If the `orders` service has instances on several nodes, ingress can route to an appropriate healthy instance without exposing that topology to the caller.

### Role

Provide a built-in path from external clients to logical Grove services.

Ingress belongs conceptually to the **cluster routing layer**, not to a specific process or Grovlet.

---

## Grove RPC

Grove RPC handles east-west service-to-service communication.

A caller addresses the logical destination service and method. Grove resolves that identity to an appropriate running service instance.

The caller does not need to know the destination's:

- IP address
- hostname
- node
- process

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
- application processes
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
- application processes
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

A developer should be able to target a logical service and let Grove locate the node and process running it.

Grovlets can expose a debugging endpoint and proxy protocols such as DAP/Delve to that process. A debugger pauses the whole process, so isolating a service in its own worker lets it be debugged without pausing its neighbours.

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
            ├── App runtime                                   └── App runtime
            │   ├── frontend                                      ├── orders
            │   └── orders                                        └── inventory
            └── Worker (isolated)
                └── payments
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
| **Service** | Application logic and logical identity, declared as a component |
| **Handler** | One method of a service, placed individually when declared |
| **Service instance** | One running copy of a service |
| **Application runtime** | The shared process that runs a node's services by default |
| **Worker** | A dedicated process for one isolated service |
| **Grovlet** | Node-local supervision; never runs application code |
| **Node** | Compute participant in the cluster |
| **Cluster** | Global coordination and application runtime boundary |
| **Ingress** | External client → service routing |
| **Grove RPC** | Service → service routing |
| **System NATS** | Grove control traffic |
| **Data NATS** | Application data-plane communication |
| **SDK** | Developer-facing distributed capabilities |
| **CLI** | Developer and operator control surface |

The shortest useful Grove mental model is:

> **Services describe the application. Application processes execute them. Grovlets supervise those processes. Nodes provide compute. The cluster coordinates everything. Ingress and RPC route by service identity.**

---

## From laptop to cluster

The same concepts should survive as the application grows.

### Local

```text
Application binary
└── Grovlet
    └── Application runtime
        ├── frontend
        ├── orders
        └── payments
```

### Small cluster

```text
Node A                         Node B
└── Grovlet                    └── Grovlet
    └── App runtime                └── App runtime
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

Start here to understand **what the pieces are**. The [glossary](glossary.md) defines each term exactly, and [packages](architecture/packages.md) maps each piece to the package that owns it. Continue into the architecture docs to understand **how Grove implements them**.
