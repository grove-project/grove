# Task 020 — JetStream-backed control state and cluster restart

Status: TODO
Depends on: 019

## Goal
Prove that Grove's authoritative JetStream/KV control state survives a complete Grovlet restart and reconstructs the desired deployment.

## Scope
- Persist the embedded System NATS/JetStream state in the node/test state directories required by the MVP topology.
- Restart the local Grove cluster using the same persisted NATS/JetStream state.
- Reconnect Grovlets to the restored System NATS control plane.
- Recover membership/control metadata needed by the current MVP.
- Re-run reconciliation from the restored desired state and reconstruct the application deployment.

## Architectural constraint
Durability for Grove control-plane metadata comes from System NATS JetStream/KV. Do not introduce a separate Grove metadata database, custom write-ahead log, etcd dependency, or Grove-owned Raft implementation for this task.

## Out of scope
Cross-version schema migration, remote object storage, production disaster recovery, NATS backup/restore tooling, and application-data durability.

## E2E
Deploy the reference app, verify desired state and placement in JetStream/KV, stop all Grovlets cleanly, restart the cluster using the same test state directories, wait for the JetStream/KV-backed control state to become available, wait for deployment reconstruction, and verify the Workflow -> Greeter flow.

## Done
`go test ./...` passes.