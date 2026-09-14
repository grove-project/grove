# Bootstrap E2E Validation

Status: Concept / evolving design  
Purpose: Define how Grove proves that intra-cluster and cross-cluster service dependencies are actually usable before a deployment is considered ready.

## Principle

Every Grove service may ship executable end-to-end validation flows with the application binary.

These tests are not only developer or CI tests. Grove can execute them during cluster bootstrap, rollout, recovery, or federation join to verify that the environment in which the service is running satisfies the service's real dependency contract.

The core rule is:

> **A service is not ready merely because its process started. Grove should be able to prove its required service graph works by running application-owned E2E checks.**

This applies to dependencies inside one Grove cluster and to dependencies reached through Grove Federation.

## Intra-cluster validation

A service can define an E2E flow that exercises the Grove services it depends on inside the same application cluster.

For example:

```text
checkout
   │
   ├──► inventory
   └──► payments
```

The checkout bootstrap test may perform a harmless synthetic transaction that proves:

```text
checkout
  → resolve inventory
  → reserve synthetic item
  → resolve payments
  → execute synthetic authorization
  → verify expected result
```

Passing the test proves more than process health. It verifies service discovery, routing, RPC compatibility, permissions, required state, and the relevant runtime capabilities across the deployed graph.

## Cross-cluster validation

Federated services use the same model for dependencies exported by another Grove application cluster.

```text
Groveshop cluster                    Fraud cluster

checkout ── Federation NATS ───────► scoring
```

A Groveshop bootstrap test can verify that the `fraud/scoring` dependency is:

- discoverable through the federation,
- reachable,
- authorized for the calling application identity,
- protocol/version compatible,
- operational enough to satisfy the application contract.

The fact that a service appears in federation discovery is not sufficient proof that it is usable.

## Tests travel with the binary

Bootstrap E2E tests belong to the application release and should be compiled or embedded into the same Grove application binary as the services they validate.

This preserves Grove's artifact model:

```text
application binary
├── services
├── runtime
├── embedded configuration
├── operational console/actions
└── E2E contracts
```

The same contract can therefore be executed locally, in CI, during a deployment, after joining a cluster, at a customer edge, or against a federated application dependency.

## Readiness model

Grove should distinguish several stages rather than reducing readiness to process liveness:

```text
process started
    ↓
local runtime ready
    ↓
intra-cluster E2E satisfied
    ↓
required federation dependencies satisfied
    ↓
application ready
```

A service with no required cross-cluster dependency does not wait for federation validation.

Optional dependencies should be declared as optional so their failure can degrade functionality without blocking the entire application bootstrap.

## Required vs optional dependencies

The application must be able to express whether an E2E dependency is required for readiness.

Conceptually:

```text
Required
  failure → service/application remains not ready

Optional
  failure → service/application becomes degraded but may serve
```

This prevents Grove from turning every temporary federation outage into a total application outage while still enforcing dependencies that are genuinely required for correctness.

## Bootstrap execution

A possible lifecycle is:

```text
start Grove runtime
    ↓
start required services
    ↓
wait for basic service registration/readiness
    ↓
run intra-cluster bootstrap E2E flows
    ↓
join/discover federation if configured
    ↓
run required cross-cluster E2E flows
    ↓
publish application READY
```

Tests should execute only after the runtime has enough topology to route the calls they exercise.

## Rollouts and recovery

The same E2E contract should be reusable as a health gate during:

- first bootstrap,
- configuration changes,
- version rollouts,
- rollback decisions,
- service relocation or migration,
- recovery after node loss,
- federation join/rejoin,
- edge reconnection.

This avoids creating separate deployment-specific health scripts that can drift away from the tests developers already use.

## Safe tests

Bootstrap E2E tests must be designed for production execution.

They should use synthetic or explicitly isolated data, be bounded by timeouts, be repeatable, avoid destructive side effects, and clean up temporary state where appropriate.

Where a dependency cannot safely execute a real operation, it may expose a test capability that exercises the same critical path without mutating production business state.

## Relationship to placement validation

Placement validation and bootstrap E2E testing answer different questions.

```text
Placement validation
    Can this particular node host the service?

Bootstrap E2E validation
    Does the deployed service and its dependency graph actually work?
```

A node may pass placement validation while the application still fails bootstrap E2E because a remote dependency, authorization rule, schema, configuration value, or federated service is unavailable.

Both are required to give Grove meaningful readiness semantics.

## Relationship to resilience testing

Bootstrap E2E flows should be the same application-owned flows that Grove can later reuse for resilience testing.

```text
normal bootstrap E2E
        ↓
known-good application flow
        ↓
Grove injects failure / migration / restart / network disruption
        ↓
same E2E verifies correctness and SLA
```

This makes the E2E contract a reusable executable description of what the application requires to function.

## Operational visibility

The Grove console should expose bootstrap validation as part of application health rather than as opaque test output.

Example:

```text
Bootstrap validation

✓ inventory reservation        local cluster
✓ payment authorization        local cluster
✓ fraud scoring                federation: fraud/scoring
✗ customer profile             federation: crm/profile
  cause                        authorization denied
  requirement                  optional

Application                   DEGRADED
```

Operators should be able to see which dependency failed, whether it is local or federated, whether it blocks readiness, and the last successful validation time.

## Architectural invariants

- Grove services may carry application-owned E2E contracts in the application binary.
- The same E2E flows can be executed during bootstrap as readiness validation.
- Both intra-cluster and cross-cluster dependencies can be validated.
- Discovery alone is never considered proof that a federated dependency is usable.
- Required dependency failures block readiness; optional dependency failures produce a degraded state.
- Bootstrap E2E tests must be bounded, repeatable, and safe to execute in production environments.
- Placement validation and E2E dependency validation remain separate mechanisms.
- The same E2E flows should be reusable by Grove's resilience and migration testing machinery.
