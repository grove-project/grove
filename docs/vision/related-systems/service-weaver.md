# Service Weaver and Grove

Service Weaver is the closest historical predecessor to Grove at the application-artifact and programming-model level. The relationship is more useful as intellectual lineage than as a simple competitor comparison: Service Weaver demonstrated that a modular Go application can be compiled as one application and distributed dynamically by a runtime.

Grove starts from much of the same philosophy, while deliberately choosing different boundaries for the developer model and the runtime.

## Shared philosophy

Both systems reject the idea that a distributed application must inherently become a collection of independently built and operated service artifacts.

The common idea is:

> **One application artifact → multiple runtime instances → distributed application.**

Application code and a Go-native distributed runtime can live together. The runtime can then decide where parts of the application execute without forcing every logical service to become its own separately packaged deployment unit.

This is one of the central ideas behind Grove as well.

## Where Grove deliberately diverges

Service Weaver introduced a component programming model built around Go interfaces and generated RPC plumbing. Component calls are intentionally made largely transparent with respect to whether the target is local or remote.

Grove deliberately avoids that model. Its developer principles are:

- no required service interfaces
- no generated RPC code
- ordinary concrete Go types remain ordinary Go types
- distribution primitives are explicit in source
- placement and topology remain runtime concerns

The guiding principle is:

> **Developer controls semantics. Grove controls topology.**

A local Go call should remain an ordinary Go call. A call that may cross a process or network boundary should visibly use a Grove primitive because latency, failure and retry semantics are fundamentally different.

The goal is incremental adoption: existing Go applications should be able to introduce Grove at selected service boundaries rather than being restructured around a new component abstraction.

## The Grove artifact

A Grove application artifact is intended to contain:

- Grove runtime
- application services
- built-in ingress/runtime capabilities
- embedded customer configuration
- configuration/schema version information

Customer configuration can be compiled, compressed and embedded into a reserved section of the artifact. Configuration changes therefore become versioned application deployments rather than mutable configuration files scattered around the environment.

The intended property is:

> **The artifact is the complete description of what should run.**

Every Grove node runs the same versioned application artifact. Grove decides which services execute on which nodes and can optimize placement and co-location while preserving the application's explicit distribution semantics.

## Kubernetes is a substrate

Grove can start on Kubernetes without making Kubernetes part of its application programming model.

A simple deployment is:

```text
3 Kubernetes pods → 3 Grove nodes → 1 Grove application cluster
```

Each pod runs the same Grove application artifact. The Grove instances form their own logical cluster and Grove manages application placement and routing across them.

The equivalent deployment without Kubernetes is:

```text
3 VMs → 3 Grove nodes → 1 Grove application cluster
```

The Grove application/runtime model remains the same; only the infrastructure hosting each node changes.

## Built-in ingress

Ingress is intended to be a Grove cluster primitive.

On Kubernetes, every Grove pod can expose the Grove ingress endpoint while one Kubernetes Service selects all Grove pods:

```text
Client
  ↓
Kubernetes Service
  ↓
Any Grove node
  ↓
Grove routing
  ↓
Target service
```

Kubernetes chooses which Grove node initially receives a connection. Grove chooses where the target application service actually executes.

If a request lands on node A while `Orders` runs on node C, Grove routes it to node C. If Grove later moves `Orders` to node B, neither the client nor the Kubernetes Service needs to change.

Outside Kubernetes, the outer load-balancing mechanism can change while Grove's internal ingress semantics remain the same.

## Runtime scope

Grove aims to put more of the operational application lifecycle inside the application-aware runtime itself. This includes:

- cluster membership and consensus-backed state
- service placement and co-location
- cluster-aware routing and ingress
- storage and durability primitives
- lifecycle and upgrades
- process and potentially Firecracker microVM isolation
- stateful migration where feasible
- cluster-aware Delve/DAP debugging
- E2E testing combined with runtime-generated resilience scenarios

This is a broader runtime boundary than merely deciding where application components execute.

## Migration and isolation

Grove intends to support ordinary process isolation and potentially Firecracker microVMs. Firecracker creates a path toward pause/snapshot/transfer/resume migration for appropriately written services.

The Grove SDK should therefore encourage migratable application patterns and make dependencies on non-migratable resources explicit.

Where feasible, Grove's long-term goal is not merely to relocate a logical service but to preserve actual execution state while moving it.

## Testing and debugging as runtime capabilities

Grove treats developer operations as part of the runtime rather than separate deployment tooling.

Cluster-aware Delve/DAP debugging should let an IDE connect through a stable Grove/Grovlet endpoint while Grove resolves the process and node currently containing the target service.

Likewise, `grove test` should combine application E2E flows with a resilience matrix. Developers describe functional behavior and relevant SLAs; Grove exercises those flows while introducing failures and verifies that the application remains correct.

## Comparison summary

| Dimension | Service Weaver | Grove |
|---|---|---|
| Core philosophy | Modular Go application distributed by a runtime | Ordinary Go application distributed by an application-aware runtime |
| Artifact | Application + Weaver runtime | Application + Grove runtime + embedded customer configuration |
| Service abstraction | Weaver components/interfaces | Ordinary concrete Go services |
| RPC plumbing | Generated | Explicit SDK primitives, no generated code |
| Local vs remote | Intentionally mostly transparent | Explicit in source |
| Placement | Runtime controlled | Runtime controlled, locality/co-location first-class |
| Kubernetes | Production deployer/substrate | Optional substrate; pod maps naturally to Grove node |
| Ingress | Application listeners + deployer exposure | Built-in cluster-aware ingress/routing |
| Isolation | Process/deployment based | Process + potential Firecracker microVM |
| Migration | Component placement | Potential execution-state migration |
| Configuration | External deployment configuration | Customer configuration embedded in versioned artifact |
| Debugging | Conventional/runtime tooling | Cluster-aware Delve/DAP intended as runtime capability |
| Resilience testing | Not central to programming model | E2E × failure matrix intended as first-class capability |

## What Grove inherits from Service Weaver

Service Weaver validated several ideas that are fundamental to Grove:

1. A distributed Go application does not need to be authored as a collection of independently deployed microservices.
2. Application components can remain together in one application artifact.
3. Runtime-controlled placement can separate logical application structure from physical topology.
4. Locality can be optimized by the runtime rather than hard-coded into deployment manifests.

Those are important validations of Grove's direction.

## What Grove learns from Service Weaver

The critical lesson is that reducing operational complexity should not require replacing ordinary Go development with a new application programming model.

Grove therefore keeps what was most compelling about Service Weaver — the single application artifact and runtime-controlled topology — while intentionally rejecting much of the machinery introduced to achieve it.

The Grove thesis is:

> **Keep the application ordinary Go. Make distribution explicit. Put the operational complexity into the runtime.**

That makes Service Weaver more than a competitor to Grove. It is an important predecessor whose successes validate Grove's core philosophy and whose trade-offs help define where Grove should go differently.
