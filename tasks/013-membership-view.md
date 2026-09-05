# Task 013 — Grove membership in JetStream/KV

Status: TODO
Depends on: 012

## Goal
Represent Grove node membership as authoritative replicated control-plane state in System NATS JetStream/KV.

## Observable behavior
After convergence, every Grovlet observes the same Grove membership records containing node IDs and advertised endpoints.

## Scope
- Enable the JetStream/KV capability required by the System NATS control plane.
- Define the minimum membership KV schema/key layout.
- Register each Grove node in the membership bucket/state.
- Read/watch membership through JetStream/KV so all Grovlets converge on the same authoritative view.
- Expose a machine-readable membership API for tests and future CLI use.

## Architectural constraint
JetStream/KV is the replicated consensus-backed source of truth for Grove membership metadata. Do not maintain a separate authoritative membership database or implement custom Raft.

NATS server-cluster membership and Grove node membership are related bootstrap concerns but are not the same model; this task defines Grove's logical node membership on top of System NATS.

## Out of scope
Persistent membership history, failure recovery, placement, application service discovery, and a custom leader-election protocol.

## E2E
Launch 3 real Grovlets, wait for all three membership records to appear through JetStream/KV, and assert every Grovlet reports the same logical membership view using condition waits only.

## Done
`go test ./...` passes.