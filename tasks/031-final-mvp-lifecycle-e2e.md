# Task 031 — Final Grove Shop MVP lifecycle E2E

Status: TODO
Depends on: 020, 024, 028, 030

## Goal
Create one automated regression test demonstrating the complete Grove MVP demo contract.

## Required reading
- `demo/README.md`
- `demo/ARCHITECTURE.md`
- `demo/UI.md`
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`
- `demo/IMPLEMENTATION_GUIDE.md`

## E2E
Through Go testing + `grovetest`:

1. Build the Grove Shop artifact with embedded Web UI assets.
2. Embed the good `acme` customer config and produce Artifact A.
3. Launch a real multi-Grovlet cluster on one host.
4. Deploy Artifact A.
5. Wait for Grove Shop and cluster health.
6. Verify the Web component serves the embedded single-page UI from the Grove cluster.
7. Verify the Grove structured status endpoint/read model reports nodes, component placement, health, active artifact, and config identity.
8. Execute a full order flow across Grove Shop services and verify `Created -> Reserved -> Paid -> Shipping -> Completed`.
9. Prove at least one application call crosses Grovlet process boundaries.
10. Exercise the existing node-failure/recovery path and re-prove an order flow.
11. Stop and restart the entire cluster from durable state and prove desired deployment reconstruction.
12. Produce Artifact B from the same Grove Shop application with the broken customer config from `demo/CONFIGURATION.md`.
13. Deploy Artifact B as the candidate while Artifact A remains known-good.
14. Observe candidate rollout through the same structured state consumed by the Web UI polling contract.
15. Observe Inventory fail because the embedded configuration is invalid.
16. Observe Grove mark the candidate unhealthy and record the rollback/rejection reason.
17. Verify authoritative active artifact returns to/remains Artifact A and its original config identity.
18. Execute another successful order after rollback.
19. Run the MVP resilience workflow.
20. Finish with a healthy cluster and clean up all child processes and temporary artifacts.

## UI polling proof
A headless browser is not required unless already justified by the implementation. The E2E must prove the backend contract that makes the browser live view possible: repeated reads of the structured Grove status endpoint/read model can observe rollout state changes without scraping logs.

The actual Web UI must continuously poll this contract approximately every 500 ms to 1 second while open and display Orders + Cluster Status on the same screen.

## Minimal demo UX
The implemented CLI and artifact workflow should allow the human demo to converge on:

```bash
grove deploy --config configs/acme.yaml
grove deploy --config configs/acme-broken.yaml
```

The browser remains open between commands and requires no manual refresh to observe candidate startup, failure, rollback, and final health.

## Constraints
- No manual acceptance steps.
- No shell orchestration.
- No Docker requirement.
- No fixed sleeps.
- All child processes are launched by the Go harness.
- Failures dump useful node, placement, deployment, artifact/config, and component diagnostics.
- The UI is an observer only; health detection and rollback must work with no browser connected.
- Do not add debugging/DAP; that is outside MVP v1.

## Done
`go test ./...` passes. This marks the planned Grove MVP complete.