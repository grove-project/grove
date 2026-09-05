Application Model & SDK

Status: Draft / evolving

Purpose  
Define how developers describe Grove applications and interact with runtime capabilities from Go code.

Application Model  
A Grove application is one versioned release containing multiple cooperating services. Services belong to the same application lifecycle even when Grove places them in different workers or nodes.

The SDK should let developers declare services, dependencies, communication endpoints, durability needs, and runtime capabilities without exposing unnecessary cluster internals.

Core SDK Directions  
• Service registration and discovery.  
• Version-aware communication between services.  
• Request/reply and messaging primitives.  
• Access to KV, streams, object storage, and other Grove data services.  
• Explicit durability / commit semantics where useful.  
• Health and readiness reporting.  
• Runtime metadata such as service identity, version, node, and replica.  
• Hooks for graceful shutdown and upgrade.

Design Principle  
The SDK should describe application intent while Grove owns placement, supervision, routing, and recovery.

Open Questions  
• Exact service declaration API.  
• Static registration vs runtime registration.  
• How much placement affinity developers may request.  
• Exact durability API exposed to application code.  
• Extension/plugin API for Grove-native infrastructure services.

Non-Intrusive SDK Direction  
Grove should integrate at the composition boundary rather than becoming the application's programming model. Business packages should remain ordinary Go wherever practical.

Preferred shape:  
• Application-owned interfaces define service contracts.  
• Normal constructor injection remains the default dependency pattern.  
• Grove supplies implementations, remote proxies, storage adapters, messaging adapters, and runtime wiring.  
• Grove-specific APIs are opt-in when a capability genuinely requires them.

Native Service Contracts  
The developer should use the application's original Go package, request/response types, enums, comments, and interfaces in both application code and tests. Grove should avoid requiring a second developer-facing IDL or generated application model.

If code generation is required by Go's static type system, it should generate disposable proxy implementations that satisfy application-owned interfaces, not new developer-facing domain types.

Typed Testing Experience  
A test should be able to obtain a typed client for an existing application interface and retain normal gopls autocomplete, compile-time checking, GoDoc, and refactoring support.

Repository Structure  
Grove should not require a services/, internal/, cmd/, or tests/e2e/ layout. Existing Go repositories should be first-class through a lightweight grove init path. Grove should be opinionated about distributed contracts and runtime behavior, but minimally opinionated about package and directory organization.

Litmus Test  
If removing Grove requires rewriting the application's business logic, the SDK is too intrusive.

Migratable Application Contract  
Grove should make migratability an explicit, testable property of a service. The preferred SDK surface should steer developers toward capabilities that remain valid when a Firecracker-backed worker is frozen, moved to another node, restored, and resumed.

Preferred migratable primitives  
• grove.Storage(...) for state that must remain available after relocation.  
• Grove service clients and messaging for communication whose routing can follow the service.  
• Grove-managed clocks, deadlines, leases, and timers where suspension semantics matter.  
• Grove networking for listeners and connections whose virtual network identity can move with the workload.  
• Runtime lifecycle context for the small number of services that need migration-aware preparation or recovery.

Mobility declaration  
A service should be able to declare its expected mobility behavior, for example:

  grove.Service(checkout.Run, grove.Mobility(grove.Seamless))

Candidate mobility classes:  
• Seamless: suspension + relocation + resume should not cause observable application failure.  
• Reconnect: in-memory execution state can move, but external resources are expected to reconnect after resume.  
• Pinned: the service uses a node-local or physical capability and is not currently eligible for transparent relocation.

Migration lifecycle hooks  
Hooks such as BeforeMigration and AfterMigration may exist for exceptional cases, but they should not be required for normal applications. Grove-managed capabilities should handle migration transparently whenever possible.

Capability tracking  
The runtime should track resources acquired by each service and derive or validate its effective mobility. Examples that can degrade mobility include host filesystem paths, host Unix sockets, AF_PACKET/raw host networking, direct physical devices, passthrough accelerators, or other node-specific state. Tooling should identify the exact reason rather than simply mark a service non-migratable.

grove test integration  
Migration should become part of resilience testing. Given an existing E2E flow, Grove should be able to inject a migration while the flow is active:

  start E2E flow  
      → freeze service  
      → snapshot execution state  
      → relocate Node A → Node B  
      → restore + resume  
      → continue the same flow  
      → verify correctness and SLA

This turns a mobility declaration into an executable contract. A developer focuses on application behavior; Grove exercises the migration matrix automatically.

Design constraint  
The SDK must encourage portability without becoming intrusive. If making a service migratable requires Grove-specific business logic throughout the application, the abstraction has failed. Migratability should come primarily from choosing portable runtime capabilities at the edges.

Opinionated Service Capabilities  
Grove's SDK should offer a set of explicit, opinionated design patterns that a service can adopt incrementally. A plain Go service remains valid and minimally coupled to Grove. When a developer chooses a Grove pattern, that choice gives the runtime additional semantic knowledge about the service.

The core contract is: explicit adoption of a pattern unlocks concrete runtime guarantees, optimizations, testing behaviors, diagnostics, and CLI operations. Grove should make the added value visible rather than treating SDK usage as an invisible implementation detail.

Patterns are executable semantics, not labels  
A capability must be a real programming/runtime abstraction, not metadata equivalent to a Kubernetes label. Adopting a capability changes what Grove can safely assume and do. For example, durable execution means Grove owns enough execution state to recover or resume work; a parallel scheduling hint tells Grove which units of work may safely execute concurrently.

Example  
A service may combine capabilities such as durable execution and partition-aware parallel scheduling:

  durable := grove.Durable(service)  
  durable.Register("process-order", processOrder,  
      grove.ParallelBy(func(req Order) string {  
          return req.CustomerID  
      }),  
  )

From this explicit choice Grove can infer that executions are durable, different customer partitions may run concurrently, same-customer work may require serialization, execution state can be inspected, failure can be injected during an E2E flow, and recovery/migration tooling can reason about the service's state.

Capability-driven CLI  
The CLI should reflect the semantics a service has opted into. `grove service <name>` should expose both the capabilities and the operational value they unlock. Capability-specific commands should appear naturally where useful.

Example service view:

  Service: x

  Capabilities  
    Durable Execution       recovery, replay, execution inspection  
    Parallel Scheduling     automatic concurrency optimization  
    Migratable              placement changes / migration testing  
    Idempotent RPC          safe automatic retries  
    Replicated State        failover without local-state loss  
    SLA Defined             resilience validation and SLO monitoring

Possible capability-specific operations include durable execution listing/inspection/retry, parallelism and bottleneck inspection, migration eligibility and reasons, and resilience-test scenarios derived from the service's declared semantics.

Progressive adoption model  
Grove should support a progression rather than requiring a single application model:

• Level 0 — Ordinary Go service: Grove runs, routes, observes, deploys, and supervises it.  
• Level 1 — Grove-aware service: explicitly uses Grove communication, storage, lifecycle, or other runtime primitives.  
• Level 2 — Pattern-aware service: adopts capabilities such as durable execution, partitioned processing, migratable state, idempotency, actor-style ownership, replicated state, backpressure, or SLA declarations.  
• Level 3 — Highly optimizable service: Grove understands enough semantics to optimize placement, concurrency, recovery, migration, and resilience testing automatically.

Capability composition  
Capabilities should compose. A service may opt into multiple patterns, and Grove should reason about their combined semantics. For example, Durable Execution + Parallel Scheduling + Migratable can allow Grove to optimize concurrency while retaining recoverability and safely exercising migration during E2E tests.

The capability model should also surface conflicts and degradations. If a service declares migratability but acquires a host-local resource, Grove should explain which capability was weakened and why.

CLI as the capability feedback loop  
The CLI is the primary feedback mechanism showing developers that adopting an opinionated Grove pattern bought them something. A command such as `grove service capabilities x` should answer two questions: what does Grove know about this service, and what can Grove now safely do because of it?

This creates a positive adoption loop:

  developer adopts explicit pattern  
      → Grove gains semantic knowledge  
      → runtime unlocks stronger behavior  
      → tests exercise stronger guarantees  
      → CLI exposes new insight and operations  
      → developer sees the concrete value of the pattern

Design principle  
Grove should be minimally opinionated about how developers organize ordinary Go code, but deliberately opinionated about distributed-system patterns where explicit semantics let the platform provide substantial added value. Distribution remains visible: the developer explicitly chooses the primitive, while Grove turns that explicit intent into stronger guarantees and better tooling.

Capability Trade-offs and Derived Properties  
Every Grove capability should communicate both what it unlocks and what it costs. Patterns are architectural trade-offs, not universally good features. The CLI and development tooling should expose benefits, costs, constraints, compatible workloads, and likely conflicts with declared SLOs. For example, Durable Execution provides recovery, replay, and resumability, but persistence on the execution path can add latency and therefore may be a poor fit for real-time, low-latency processing.

Grove should reason about these trade-offs using application intent. If a service declares a strict latency SLO while using Durable Execution on its hot path, Grove should warn about the tension and suggest alternatives such as moving durable work behind an asynchronous boundary.

Capabilities should also imply properties that Grove can guarantee rather than forcing developers to declare redundant capabilities. Durable Execution is the key example: because Grove owns enough durable execution state to recover and resume the logical operation, that execution is automatically migratable between nodes. A separate Migratable declaration should not be required for durable executions.

Derived properties  
  Durable Execution  
      → recoverable  
      → resumable  
      → execution-migratable  
      → replayable / inspectable

  Idempotent Operation  
      → safely retryable

  Partitioned Execution  
      → safely parallelizable across independent partitions

  Real-Time Path  
      → favors minimum execution-path overhead

The CLI should distinguish explicitly selected capabilities from properties derived by Grove. This keeps the developer-facing model small and prevents developers from specifying facts the runtime can already prove.

Execution migration vs service migration  
Execution migration and whole-service migration remain distinct concepts. Durable Execution provides semantic migration: Grove can resume the logical operation elsewhere from durable state. Whole-service or process migration is broader and may involve in-memory state, open connections, local resources, devices, or a Firecracker snapshot. Those resources can still constrain transparent service migration even when individual durable executions are migratable.

Capability design rule  
A new capability should exist only when explicit adoption gives Grove meaningful semantic knowledge that changes runtime behavior, testing, diagnostics, optimization, or available operations. Properties that can be safely inferred from another capability should be derived automatically rather than exposed as additional configuration.  
