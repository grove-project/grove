# Grove — Positioning & Differentiation

Grove is an application-aware runtime for distributed Go applications. It separates the **logical application topology** developers write from the **physical execution topology** used at runtime.

The shortest positioning is:

> **Nomad schedules workloads. Service Weaver distributes components. Grove schedules application topology.**

Service Weaver is Grove's closest philosophical predecessor: it demonstrated that a modular Go application can remain one application artifact while a runtime distributes its components. Nomad is useful from the opposite direction: it shows the boundary between infrastructure orchestration and Grove's application-aware runtime.

## Comparison

| Dimension | Grove | Service Weaver | Nomad |
|---|---|---|---|
| Primary abstraction | Application-aware execution runtime | Distributed Go component framework/runtime | Workload orchestrator |
| Input to scheduler/runtime | Logical application topology | Weaver components | Jobs and tasks |
| Primary decision | How application components should physically execute and where they should run | Where components should run | Which node should run each allocation |
| Application artifact | Application + Grove runtime + embedded customer configuration | Application + Weaver runtime | Arbitrary packaged workloads |
| Service abstraction | Ordinary concrete Go services | Weaver components/interfaces | Opaque workload/task |
| Distribution semantics | Explicit SDK primitives | Mostly transparent component calls | External to application code |
| Generated RPC code | No | Yes | N/A |
| Runtime-controlled placement | Yes — including locality/co-location | Yes | Yes, at workload level |
| Execution boundaries | Goroutine, process, potential microVM, node | Process/deployment boundaries | Process/container/driver workload |
| Built-in cluster-aware ingress | Yes | Application listeners + deployer exposure | Typically external/service-discovery ecosystem |
| Configuration model | Customer config embedded in versioned artifact | External deployment configuration | Job/config mechanisms |
| Same artifact across cluster | Core design principle | Application binary deployed across runtime | Not required |
| Kubernetes relationship | Optional substrate; Grove can run inside it and interoperate with conventional workloads | Supported deployment environment | Alternative/complementary infrastructure orchestrator |
| Stateful migration direction | Runtime/SDK designed; potential pause/snapshot/transfer/resume for suitable workloads | Component re-placement, not execution-state migration | Workload rescheduling rather than application-state migration |
| Edge model | Edge nodes can join the same logical cluster using outbound connectivity | Not a primary design focus | Infrastructure-oriented multi-node scheduling |
| Debugging | Cluster-aware Delve/DAP intended as runtime capability | Conventional/runtime tooling | Infrastructure/workload-level tooling |
| Resilience testing | Application E2E × runtime-generated failure matrix intended as first-class capability | Not central to programming model | Not application-semantic testing |
| Runtime understanding | Application graph, dependencies, placement and application-aware capabilities | Component graph | Resource/workload declarations |
| Core promise | Write the logical application; Grove chooses and evolves its physical execution topology | Write modular Go components; runtime distributes them | Declare workloads; Nomad schedules and operates them |

## What Grove takes from Service Weaver

Service Weaver validated a foundational idea behind Grove:

> **One application artifact → multiple runtime instances → distributed application.**

A distributed Go application does not inherently need to become a collection of independently built and operated microservices. Logical application structure can remain separate from physical placement, and the runtime can optimize locality without hard-coding deployment topology into the application.

Grove deliberately diverges in the developer model. Service Weaver uses interfaces and generated RPC plumbing and intentionally makes local and remote component calls largely transparent. Grove instead follows three rules:

- no required service interfaces
- no generated RPC code
- distribution boundaries are explicit in source

The principle is:

> **Developer controls semantics. Grove controls topology.**

A local Go call remains an ordinary Go call. A call that may cross a process or network boundary visibly uses a Grove primitive because latency, failure and retry semantics are fundamentally different.

## Why Grove is not Nomad

Traditional orchestrators such as Nomad generally begin with already-packaged workloads. Their question is:

> **Where should this workload run?**

The application's internals are mostly opaque to the scheduler.

Grove moves the orchestration boundary into the application. Its question is:

> **How should this application execute right now?**

For an application topology such as:

```text
API → Parser → Rules Engine → KV
```

Grove might initially co-locate all four components because they communicate heavily. As load, isolation requirements, locality, resource pressure or failure domains change, Grove may move components across processes, microVMs or cluster nodes without changing the application's logical structure.

That is why Grove can superficially resemble Nomad: both contain nodes, scheduling, health monitoring, consensus, networking and workload recovery. Those mechanisms are infrastructure inside Grove, not Grove's defining abstraction.

## The Grove artifact

A Grove application artifact is intended to contain the runtime, application services, built-in ingress/runtime capabilities, embedded customer configuration and configuration/schema version information.

The intended property is:

> **The artifact is the complete description of what should run.**

Every Grove node runs the same versioned application artifact. Grove determines which services execute where and can optimize placement and co-location while preserving the application's explicit distribution semantics.

A simple Kubernetes deployment can therefore look like:

```text
3 Kubernetes pods → 3 Grove nodes → 1 Grove application cluster
```

and the same application can run without Kubernetes:

```text
3 VMs → 3 Grove nodes → 1 Grove application cluster
```

The Grove application model does not change; only the infrastructure substrate does.

## Positioning guidance

Avoid leading with **lightweight cluster**, **scheduler**, **single binary**, **NATS/Raft**, or **alternative to Kubernetes**. Those describe mechanisms and immediately place Grove in the infrastructure-orchestrator category.

Lead with the developer property:

> **Write the logical application once; let Grove choose and evolve its physical execution topology.**

Then explain the cluster, ingress, storage, migration, debugging, configuration and resilience capabilities that make that possible.

Service Weaver explains Grove's programming-model lineage. Nomad explains the orchestration boundary Grove moves beyond.

The Grove thesis is:

> **Keep the application ordinary Go. Make distribution explicit. Put the operational complexity into the runtime.**
