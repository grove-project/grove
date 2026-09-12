# Kubernetes → Grove: Gradual Migration and Resource Utilization Demo

## Purpose

This demo shows how an existing Go microservices application can adopt Grove gradually **inside the same Kubernetes cluster** while preserving application behavior.

The primary goal is not to demonstrate replacing Kubernetes. It is to demonstrate that Grove can use Kubernetes as a compute/node allocator while improving application-level resource utilization.

> **Kubernetes allocates compute. Grove allocates the application.**

The demo must compare the **same Go application, under the same workload, before and after Grove adoption**.

## Starting point — conventional Kubernetes microservices

Run Grove Shop, or an equivalent multi-service Go application, conventionally on Kubernetes:

```text
Kubernetes
├── web pod
├── orders pod
├── inventory pod
├── payment pod
└── shipping pod
```

Each service has its own Deployment/Pod, resource requests, replicas, process, and Kubernetes Service where required.

This establishes the baseline.

Record at least:

- workload throughput and latency;
- Kubernetes pod count;
- requested CPU and memory;
- actual CPU and memory usage;
- resource utilization (`used / requested`);
- application health and availability;
- relevant network/process overhead.

Do not choose expected improvement numbers in advance. The benchmark must report measured results.

## Phase 1 — add Grove to the same Kubernetes cluster

Deploy a small multi-node Grove cluster as Kubernetes pods alongside the existing application.

```text
Kubernetes
├── web pod
├── orders pod
├── inventory pod
├── payment pod
├── shipping pod
│
├── Grove node A
├── Grove node B
└── Grove node C
```

At this stage Kubernetes remains responsible for allocating the compute used by the Grove nodes.

Grove services and legacy Kubernetes services must be able to coexist and communicate across the Kubernetes network.

## Phase 2 — migrate one service

Move one service, for example `inventory`, from its dedicated Kubernetes deployment into Grove.

```text
orders pod
    │
    ▼
Kubernetes service / compatibility boundary
    │
    ▼
Grove ingress
    │
    ▼
inventory running in Grove
```

Existing callers should not need to migrate at the same time.

The migrated service may also continue to call legacy Kubernetes services. Migration must therefore not require moving an entire dependency graph at once.

Measure resource usage again with the same workload.

## Phase 3 — progressively absorb services

Repeat the process for additional services:

```text
0% → 25% → 50% → 75% → 100% Grove
```

At every step:

1. keep the workload constant;
2. keep business behavior equivalent;
3. preserve the availability target;
4. record Kubernetes requested and actual resources;
5. record Grove allocation and utilization;
6. record pod/process count;
7. record application-level performance.

This should produce a migration curve showing how utilization changes as Grove absorbs more of the application.

## Final state — Grove on Kubernetes

The final state does **not** require removing Kubernetes.

```text
Kubernetes
├── Grove node A
│   ├── web
│   ├── orders
│   ├── inventory
│   ├── payment
│   └── shipping
│
├── Grove node B
│   └── application services as placed by Grove
│
└── Grove node C
    └── application services as placed by Grove
```

Kubernetes sees a small number of relatively large Grove compute allocations. Grove owns application-level service placement and runtime behavior inside those allocations.

The responsibility split is:

```text
Kubernetes
└── allocate Grove nodes / compute envelopes

Grove
├── place application services
├── share resources across services
├── manage service lifecycle
├── route service communication
├── use application/topology knowledge for placement
└── observe and manage the application runtime
```

Kubernetes is therefore a valid long-term Grove substrate, not merely a temporary migration mechanism.

## Why utilization can improve

Conventional microservice deployments create independent resource envelopes. Each service commonly reserves CPU and memory for its own expected peaks and safety margin.

For example:

```text
orders      request 1 CPU   actual 0.20
payment     request 1 CPU   actual 0.15
inventory   request 1 CPU   actual 0.10
shipping    request 1 CPU   actual 0.20

Total requested: 4 CPU
Typical actual:   0.65 CPU
```

When those services execute inside a Grove allocation, their headroom can be shared:

```text
Grove node allocation
├── orders
├── payment
├── inventory
└── shipping

shared CPU + memory envelope
```

The important distinction is approximately:

```text
sum(individual service reservations)
```

versus provisioning toward:

```text
peak(concurrent application demand) + required safety margin
```

Grove can additionally use application-level information such as communication locality, service placement validation, workload behavior, and storage locality when deciding where services should execute.

The demo must validate these benefits empirically rather than assuming them.

## Canonical comparison

The final demo should present a side-by-side comparison of the same application:

| Metric | Kubernetes microservices | Grove on Kubernetes |
| --- | ---: | ---: |
| Workload | same | same |
| Business behavior | same | same |
| Availability target | same | same |
| Go services | same | same |
| Kubernetes pods | measured | measured |
| CPU requested | measured | measured |
| CPU used | measured | measured |
| Memory requested | measured | measured |
| Memory used | measured | measured |
| Resource utilization | measured | measured |
| Latency / throughput | measured | measured |

The headline result should be based on the measured data:

> **Same Kubernetes cluster. Same Go application. Same workload. Measure how much infrastructure Grove actually needs.**

## What this demo proves

The demo should prove all of the following together:

- Grove can be introduced into an existing Kubernetes environment without a flag-day migration.
- Services can migrate one at a time.
- Grove and legacy Kubernetes services can coexist during migration.
- Kubernetes can remain the infrastructure/node allocator permanently.
- Grove becomes the application-level scheduler/runtime inside Kubernetes-provided compute.
- Multiple application services can share Grove's resource envelope instead of requiring independent Kubernetes resource envelopes.
- The same Go application can be benchmarked before and after migration.
- Grove's resource-utilization benefit can be expressed with measured CPU, memory, pod/process, and application-performance data.

## Demo principle

Do not present this as **Kubernetes vs. Grove**.

Present it as:

```text
Kubernetes microservices
        ↓
hybrid Kubernetes + Grove
        ↓
Grove application on Kubernetes-allocated compute
```

Removing Kubernetes later is optional. The important MVP story is that Grove can deliver value **while Kubernetes stays exactly where it is**.