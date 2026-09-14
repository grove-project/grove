# Task 031 — Final Grove Shop MVP lifecycle E2E

Status: TODO — reopened after application-console design change
Depends on: 020, 024, 028, 030

## Goal
Create one automated regression test demonstrating the complete Grove MVP deployment/recovery/rollback lifecycle contract through the Grove Shop application binary. Task 032 builds on this baseline with distributed debugging.

## Required reading
- `demo/README.md`
- `demo/ARCHITECTURE.md`
- `demo/UI.md`
- `demo/CONFIGURATION.md`
- `demo/DEMO_FLOW.md`
- `demo/IMPLEMENTATION_GUIDE.md`
- `docs/cli/README.md`

## E2E
Through Go testing + `grovetest`:

1. Build the Grove Shop artifact with embedded Web UI assets and built-in Grove console/action registry.
2. Embed the good `acme` customer config and produce Artifact A.
3. Launch a real multi-Grovlet cluster on one host.
4. Deploy Artifact A through the Grove Shop application-native rollout operation.
5. Wait for Grove Shop and cluster health.
6. Verify the Web component serves the embedded single-page UI from the Grove cluster.
7. Verify the Grove structured read model reports nodes, component placement, health, active artifact, and config identity.
8. Verify the Grove Shop TUI renders the same authoritative cluster/application state without scraping logs.
9. Execute a full order flow across Grove Shop services and verify `Created -> Reserved -> Paid -> Shipping -> Completed`.
10. Prove at least one application call crosses Grovlet process boundaries.
11. Exercise the existing node-failure/recovery path through the application-native resilience operation and re-prove an order flow.
12. Stop and restart the entire cluster from durable state and prove desired deployment reconstruction.
13. Produce Artifact B from the same Grove Shop application with the broken customer config from `demo/CONFIGURATION.md`.
14. Start Artifact B as the candidate from the Grove Shop deployment operation while Artifact A remains known-good.
15. Observe candidate rollout through the same structured state consumed by both the Web UI and TUI.
16. Observe Inventory fail because the embedded configuration is invalid.
17. Observe Grove mark the candidate unhealthy and record the rollback/rejection reason.
18. Verify authoritative active artifact returns to/remains Artifact A and its original config identity.
19. Execute another successful order after rollback.
20. Run the MVP resilience workflow from the application binary.
21. Finish with a healthy cluster and clean up all child processes and temporary artifacts.

## UI/TUI proof
A headless browser is not required unless already justified by the implementation. The E2E must prove the backend/read-model contract that makes both live views possible: repeated reads can observe rollout state changes without scraping logs.

The Web UI must continuously poll this contract approximately every 500 ms to 1 second while open and display Orders + Cluster Status on the same screen.

The terminal UI must consume the same authoritative state and expose application-first navigation for cluster, services, deployments, configuration, logs, debugging, and application-specific actions.

## Minimal demo UX
The human demo should start from the application binary:

```bash
./bin/groveshop
```

Then use the TUI:

```text
Deployments > New rollout > configs/acme.yaml
Deployments > New rollout > configs/acme-broken.yaml
```

The browser may remain open to visualize the rollout simultaneously, but it is not the control surface.

For deterministic automation, use the equivalent structured actions from the same binary:

```bash
./bin/groveshop action rollout.start --config configs/acme.yaml
./bin/groveshop action rollout.start --config configs/acme-broken.yaml
./bin/groveshop action cluster.status
```

No separately required `grove` executable is part of the accepted MVP UX.

## Constraints
- No manual acceptance steps for the automated E2E.
- No shell orchestration.
- No Docker requirement.
- No fixed sleeps.
- All child processes are launched by the Go harness.
- Failures dump useful node, placement, deployment, artifact/config, component, and action diagnostics.
- The Web UI is an observer only; health detection and rollback must work with no browser connected.
- The TUI must not implement separate control logic from the structured action layer.
- Do not add debugging/DAP in this task; Task 032 owns that scope.

## Done
- `go test ./...` passes.
- The lifecycle works through the Grove Shop application binary and action registry.
- TUI/read-model behavior agrees with automated action behavior.
- This marks the deployment/recovery/rollback lifecycle baseline complete for Task 032.
