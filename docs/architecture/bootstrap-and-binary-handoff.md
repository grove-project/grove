# Bootstrap ABI and Binary Handoff

Status: Design / evolving

## Purpose

Grove can use a running cluster to roll out its successor only if an old Grove runtime can safely start and hand control to a newer runtime, and a newer runtime can hand control back to an older retained runtime during rollback.

The normal Grove SDK is allowed to evolve. The bootstrap contract is not.

> **Grove defines a tiny, intentionally stable bootstrap ABI across SDK/runtime generations.**

The bootstrap ABI exists below the application SDK. It is the compatibility boundary that lets Grove upgrade itself without requiring the current runtime to understand the successor's SDK, service model, or internal implementation.

## Layering

```text
Application code
      ↓
Grove SDK/runtime vN
      ↓
Stable Grove Bootstrap ABI
      ↓
process + Grove mesh transport
```

SDK/runtime versions may change substantially while continuing to implement the same bootstrap ABI.

```text
SDK/runtime v1  ─┐
SDK/runtime v5  ─┼── Bootstrap ABI v1
SDK/runtime v20 ─┘
```

## What the bootstrap ABI does

The bootstrap layer should know only enough to perform safe process replacement:

- identify the current and candidate artifacts,
- identify the cluster and configured node class,
- advertise bootstrap protocol versions/capabilities,
- verify compatibility before handoff,
- start a candidate process,
- provide the candidate with the minimum credentials/endpoints needed to join the existing Grove mesh,
- observe candidate bootstrap readiness/health,
- coordinate ownership handoff,
- report failure,
- support reverse handoff/rollback to a retained artifact.

It should not know application service APIs, SDK placement types, durable-execution APIs, application RPC schemas, or other fast-evolving Grove SDK concepts.

## Compatibility is bidirectional

Forward upgrade and rollback are equally important:

```text
old runtime v3  --bootstrap-v1--> new runtime v8
new runtime v8  --bootstrap-v1--> old runtime v3
```

A rollout is safe only when the current and candidate artifacts share a supported bootstrap protocol/capability set sufficient for the required handoff.

> **Rollback compatibility is a first-class bootstrap requirement, not an accidental consequence of forward compatibility.**

## Capability negotiation

Bootstrap peers should negotiate explicit protocol versions and capabilities rather than infer behavior from SDK/runtime version strings.

Conceptually:

```text
Hello
  bootstrap_versions: [1]
  runtime_version:    v8
  sdk_version:        v8
  cluster_id:         production
  node_class:         edge
  artifact_digest:    sha256:...
  capabilities:
    - artifact-transfer-v1
    - process-handoff-v1
    - readiness-v1
```

A peer may advertise newer capabilities without requiring the older peer to understand them. Handoff uses the compatible intersection.

Runtime/SDK version remains useful diagnostic information, but compatibility is determined by the bootstrap protocol and required capabilities.

## Wire-format rule

Bootstrap messages must not use arbitrary SDK/runtime Go structs as their compatibility contract. In particular, normal Gob serialization of evolving internal types would couple bootstrap compatibility to Go type evolution.

The bootstrap protocol should use a deliberately versioned, language/runtime-independent envelope with explicit fields and conservative evolution rules. The exact encoding is an implementation decision; stability and explicit versioning are the requirement.

Conceptually:

```text
BootstrapEnvelope
  protocol_version
  message_type
  message_version
  payload
```

Unknown optional fields/capabilities should be safely ignorable. Required incompatible semantics must fail before ownership handoff.

## Three compatibility layers

Grove must keep three compatibility questions separate:

1. **Bootstrap compatibility** — can the current Grovlet and candidate Grovlet launch, validate, and hand ownership between each other?
2. **Temporary control-plane compatibility** — can N and N+1 coexist long enough in the same Grove mesh/control plane to complete rollout?
3. **Application/state compatibility** — can application state and behavior transition safely between the two artifacts?

The stable bootstrap ABI solves the first problem. It does not eliminate the other two.

## Bootstrap ABI evolution

Bootstrap changes should be rare. If Grove eventually requires Bootstrap ABI v2, migration must have an overlap generation:

```text
old runtime         bootstrap v1
transition runtime  bootstrap v1 + v2
new runtime         bootstrap v2
```

This permits:

```text
v1-only -> v1+v2 -> v2-only
```

and preserves rollback across each supported transition.

Grove must not introduce a new bootstrap ABI that requires users to replace the existing cluster through an unrelated external deployment mechanism merely to upgrade Grove.

## Handoff sequence

A normal node-level handoff is:

```text
current process
    ↓ obtain + verify candidate artifact
bootstrap compatibility negotiation
    ↓
start candidate beside current process
    ↓
candidate joins existing mesh
    ↓
candidate reports bootstrap/runtime health
    ↓
application/control-plane validation
    ↓
ownership handoff
    ↓
old process exits but artifact remains rollback-ready
```

Until ownership handoff commits, the current process remains authoritative. A candidate that cannot negotiate the required bootstrap contract must be rejected before it can affect node ownership.

## Architectural invariants

- The bootstrap ABI is independent of the normal application-facing Grove SDK.
- Old runtimes do not need to understand new SDK APIs to launch new runtimes.
- New runtimes must retain the bootstrap compatibility required to roll back to supported old runtimes.
- Bootstrap compatibility is negotiated before ownership handoff.
- The bootstrap wire contract is explicitly versioned and intentionally small.
- SDK/runtime internals must not leak into the stable bootstrap protocol without a strong compatibility reason.
- The previous artifact/process remains the recovery anchor until the successor proves itself and handoff commits.
- Bootstrap ABI evolution requires an explicit overlap path.

## Design goal

The bootstrap ABI should be boring enough that Grove can keep it stable for years.

Its job is not to express Grove's application model. Its job is to guarantee that Grove can safely replace itself.
