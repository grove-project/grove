# Grove Vision

**A distributed application should still feel like one application.**

Modern teams often build business logic and then assemble a second system around it: containers, orchestration, service discovery, ingress, configuration, health checks, deployment machinery, observability, and debugging workflows.

Grove's thesis is narrower: when a set of Go services belongs to one product, the runtime can know enough about the application to make development and operations dramatically simpler.

```text
Develop → Test → Deploy → Operate → Debug → Upgrade
                     one application model
```

## What Grove should feel like

On a laptop:

```bash
$ go build -o shop .
$ ./shop
```

```text
┌ Shop ────────────────────────────────────────────────┐
│ Cluster: healthy     Nodes: 1      Version: local   │
├──────────────────────────────────────────────────────┤
│ > Services                                           │
│   Nodes                                              │
│   Deployments                                        │
│   Configuration                                      │
│   Logs                                               │
│   Debug                                              │
└──────────────────────────────────────────────────────┘
```

In production, the application grows into a cluster without becoming a different product. The same binary remains the operational entry point:

```bash
$ ./shop
```

```text
Cluster

Health      healthy
Version     v0.8.2
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Config      production-42
```

And when something changes, Grove should explain the transition, including a rollout of a new immutable configuration artifact.

That experience is the product goal.

## The two users

### Developers
Developers should spend their time on application behavior. Grove should preserve ordinary Go code, explicit distributed boundaries, normal unit testing, IDE navigation, and a short path from code to a realistic running system.

### Operators
Operators should see one coherent application: its services, nodes, versions, configuration, health, placement, rollouts, failures, handoffs, and recovery actions. Automatic behavior must be observable behavior.

Field and solution engineers inherit both concerns, especially in customer and edge environments. The same Grove model should continue to work there.

## Principles

### The application is the release unit
Application code, Grove runtime behavior, embedded configuration, and the operational console move together as one versioned artifact. Execution may span processes and machines; release identity remains coherent.

### The application binary is its operational tool
Grove should not require a separately installed operational CLI for normal use. The application binary contains the runtime, built-in operational actions, debugging entry points, configuration tooling, and developer-defined administrative actions.

The primary human interface is a live, contextual TUI. Structured actions exist underneath it for CI, scripts, tests, and other automation.

This avoids a second versioning problem where operators must pair an external CLI release with the application/runtime release they are trying to manage.

### Configuration is artifact identity
Grove configuration is immutable at runtime. Cluster, customer, environment, site, and node-class configuration is embedded into the binary artifact. If configuration changes, Grove creates a new artifact and rolls it out; it does not mutate the running binary.

Different configured artifacts may contain identical executable code. For example, cloud and edge nodes can run the same application/runtime code while their embedded configuration identifies different node zones. Code identity and configured artifact identity are therefore related but distinct.

**Configuration changes are deployments.**

### A Grove cluster can bootstrap its successor
A healthy Grove cluster already has a communication mesh and local supervisors. Grove should use that machinery to distribute and start the next artifact generation rather than require a second deployment control system.

The current Grovlet starts the successor binary beside itself, verifies that it can join and become healthy, hands responsibility over, and only then exits. Until handoff succeeds, the old process remains authoritative and rollback-ready.

This creates a self-hosting lifecycle:

```text
current cluster
      ↓ distribute successor through Grove mesh
candidate processes start beside current processes
      ↓ verify / join / health
binary handoff
      ↓
successor cluster generation
```

### The console observes and controls; it does not rewrite runtime configuration
The built-in Grove console should make configuration, artifact identity, rollout phase, health, handoff, and failure reasons easy to investigate. It may initiate a rollout or rollback, but changing configuration means supplying another immutable artifact—not editing the configuration of a running node.

### Distribution is explicit
Grove should not make remote calls look accidentally local. Developers should be able to see distributed boundaries in their code without generated RPC clients or framework-owned service interfaces.

### What you test is what you ship
Local and end-to-end testing should exercise the same important runtime paths used in deployment. Production should not introduce a hidden second application architecture.

### Start small; scale the same model
A Grove application should run as one binary on one machine and expand to multiple nodes without changing its application model.

### The runtime should explain itself
Grove should expose **state, change, cause, action, and result**. Logs, metrics, traces, health signals, placement, configuration, artifact identity, and rollout history are evidence; operators should not have to manually reconstruct basic runtime causality from them.

### Recovery is part of the product
Failure detection, restart, rollout control, binary handoff, rollback, migration, and recovery should be runtime capabilities with visible progress and outcomes—not external operational choreography.

### One operational model from cloud to edge
Edge nodes may have different immutable node configuration, connectivity, and placement constraints, but they should participate in the same logical application and expose the same health, diagnostics, debugging, rollout, and upgrade model.

### Opinionated defaults, explicit escape hatches
The common path should require little ceremony. Advanced isolation, placement, and deployment controls can exist without making them prerequisites for running the application.

### Complexity must earn its place
Grove should not become a smaller-looking general-purpose platform with the same operational surface area. Every capability should justify itself by making the application easier to develop, test, deploy, understand, operate, or upgrade.

## One lifecycle

```text
write Go
   ↓
build application binary
   ↓
./app → Test / Resilience
   ↓
./app → Deployments → Start rollout
   ↓
./app → Cluster / Services / Deployments
   ↓
./app → Services → target → Debug
   ↓
./app → Deployments → Roll back
```

For automation, those same operations are available as structured actions from the same artifact:

```bash
./app action test.resilience
./app action rollout.start ./app-next
./app action cluster.status
./app action debug.attach orders
./app action rollout.rollback
```

The exact action names are evolving, but the desired property is stable: **the tools developers use to understand Grove should agree with the tools operators use to understand Grove, and both travel with the application itself.**

## Why not just Kubernetes?
Kubernetes is a general-purpose orchestrator for independently packaged workloads. Grove intentionally starts with a narrower assumption: these services belong to one application and can share a release, runtime model, tooling, and operational context.

That narrower scope creates room for a simpler experience: one application artifact, built-in knowledge of service relationships, integrated immutable configuration and ingress, application-aware health and recovery, locality-aware placement, self-hosted binary rollout/handoff, and Grove-native diagnosis and debugging.

Grove can also run inside Kubernetes when that is the right deployment environment. The goal is not to replace every orchestrator; it is to avoid forcing application teams to assemble a platform before their application becomes operable.

## What Grove is not
Grove is not a universal workload orchestrator. It is not designed around independently versioned services with unrelated lifecycles. It does not make consensus, networking, durability, or partial failure disappear.

Instead, Grove takes responsibility for a coherent subset of those concerns so each application team does not have to assemble and operate them independently.

Grove is also under active development. Vision examples describe the intended experience; they are not a claim that every action or capability shown here is implemented today.

## Success looks like this
A meaningful Grove application can run completely on a laptop, exercise realistic failure behavior in tests, deploy as one versioned application to multiple nodes, roll configuration/software changes through its own mesh, perform safe side-by-side binary handoff, recover from common failures, and tell an operator exactly what happened.

Small deployments stay small. Larger deployments gain distribution without adopting a different application model.

## The guiding test
For every new Grove capability, ask:

> **Does this make the application easier to develop, test, deploy, understand, operate, and upgrade as one coherent system?**

If not, its complexity needs a very strong reason to belong in Grove.
