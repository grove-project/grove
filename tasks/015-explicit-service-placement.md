# Task 015 — Explicit service placement in control state

Status: TODO
Depends on: 010, 013

## Goal
Run a named service only on an eligible Grovlet and represent the resulting placement in Grove's authoritative replicated control state.

## Scope
- Add the minimum placement record model.
- Store authoritative placement metadata in System NATS JetStream/KV.
- Add the SDK plumbing required for an optional per-service placement validator.
- Treat a service with no placement validator as eligible on every Grovlet.
- Have each candidate Grovlet evaluate the service's placement validator locally.
- Represent enough eligibility result/reason in the control-plane view for placement decisions and diagnostics.
- Reject a requested placement when the target Grovlet does not pass validation.
- Allow tests/control logic to request Workflow on node A and Greeter on node B when both targets are eligible.
- Make all Grovlets observe the same resulting placement state through the replicated control plane.
- Use the placement state to route the reference application correctly.

## Placement contract
Placement validation is a hard eligibility constraint, not a scheduler preference.

The required flow is:

`candidate Grovlets -> local placement validation -> eligible set -> placement choice`

The Grovlet is authoritative for evaluating node-local environmental truth. The control plane must never place a service on a node that reports failed placement validation.

Validators must be bounded, read-only, safe to repeat, and must not perform environment setup.

## Architectural constraint
Placement is control-plane metadata and belongs in JetStream/KV. Do not create a separate local authoritative placement database or custom consensus layer.

Eligibility is evaluated locally by Grovlets; the replicated control plane consumes those results when making or validating placement decisions.

## Out of scope
General scheduler scoring, capacity balancing, affinity/anti-affinity, automatic relocation, advanced ownership arbitration, and custom Raft.

## Tests
- Service with no validator is eligible on all test Grovlets.
- Validator success makes a Grovlet eligible.
- Validator failure makes a Grovlet ineligible and preserves a diagnostic reason.
- A placement request targeting an ineligible Grovlet is rejected and the service is not started there.
- Mixed-node test: validator passes on one Grovlet and fails on another; only the passing node may host the service.

## E2E
Configure a deterministic test validator that passes on node A and fails on node B. Verify the service can be placed on A, cannot be placed on B, and that eligibility/placement state is visible consistently through the control-plane view. Then execute the cross-node reference flow successfully.

## Done
`go test ./...` passes.
