# Task 032 — Delve debugging across Grove Shop workers

Status: TODO
Depends on: 031

## Goal
Add the final MVP capability: attach ordinary Delve/DAP debugger sessions to Grove Shop services through Grove, without manually discovering worker PIDs, node addresses, or Delve ports.

The acceptance demo must attach debuggers to **two different Grove Shop workers running on two different Grovlets** and debug real Grove Shop application code.

## Required reading
- `docs/adr/009-dap-debugging-interface.md`
- `docs/developer-experience/debugging.md`
- `docs/concepts.md`
- `demo/README.md`
- `demo/ARCHITECTURE.md`
- `demo/IMPLEMENTATION_GUIDE.md`
- `demo/DEBUGGING_DEMO.md`
- `tasks/031-final-mvp-lifecycle-e2e.md`

## Scope
Implement the initial ADR-009 model only:

1. Grove resolves a selected service to its running worker and Grovlet.
2. The target Grovlet starts or attaches Delve to that worker process.
3. Grove exposes a local DAP endpoint to the developer and tunnels the DAP stream to the correct Delve instance.
4. The IDE/Delve client speaks ordinary DAP directly to that selected Delve instance.
5. Two independent debug sessions may be active at the same time for two different workers.

Do **not** implement a synthetic multi-process DAP multiplexer, cross-service step-into, global breakpoint fan-out, or debugger-state ID rewriting.

## Grove Shop deployment shape
For this task, the debugging demo deployment is deliberately topology-explicit. Run the five Grove Shop services on five dedicated Grovlets so that each service has a distinct node/worker target:

| Grovlet | Grove Shop service |
| --- | --- |
| node-1 | Web |
| node-2 | Orders |
| node-3 | Inventory |
| node-4 | Payment |
| node-5 | Shipping |

Each service must execute in its own worker OS process. The placement used by this demo may be deterministic test/demo placement; it must not introduce a general affinity/anti-affinity scheduler.

The important invariant is that Orders and Payment are provably hosted by different workers on different nodes before debugger attachment.

## CLI contract
The implementation should converge on a service-oriented command that does not expose PID or node discovery to the user:

```bash
grove debug --service orders --listen 127.0.0.1:40000
grove debug --service payment --listen 127.0.0.1:40001
```

Each command:
- resolves the current authoritative service instance;
- prints the resolved service, node, worker identity, artifact/version, and local DAP endpoint;
- starts/attaches Delve on the target node;
- keeps the DAP tunnel alive until the client disconnects or the command is interrupted;
- cleans up debugger resources on exit;
- fails with actionable diagnostics if the service is not running, has multiple ambiguous targets, Delve cannot start, or the worker exits/restarts.

If the existing CLI conventions make `--service-id` materially simpler, the implementation may support it as a lower-level selector, but the human demo must support service names.

## Debugger behavior
- Use Delve's DAP mode; Grove must not implement Go debugging semantics itself.
- Build/run the debug-demo artifact with compiler optimizations and inlining disabled where required for reliable source breakpoints (`-gcflags="all=-N -l"` or equivalent artifact-build support).
- Debugging one worker must not pause or attach to another worker implicitly.
- Health/recovery behavior while a worker is stopped at a breakpoint must be deterministic for the demo. The MVP may mark a worker as explicitly `debugging` and suppress normal failure/recovery reactions for that worker while an authorized debug session is active.
- Ending the debug session returns the worker to normal health supervision.
- DAP listeners used by Delve should not be exposed directly as public cluster ports; the developer connects to the local Grove endpoint/tunnel.

## Required demo scenario
1. Build a debug-capable Grove Shop artifact.
2. Start the five-node local Grove cluster.
3. Deploy Grove Shop with one service per node as defined above.
4. Verify all five services are healthy and record the Orders and Payment worker/node identities.
5. Start a Grove debug session for Orders and expose local DAP endpoint `127.0.0.1:40000`.
6. Start a second Grove debug session for Payment and expose local DAP endpoint `127.0.0.1:40001`.
7. Connect two independent DAP clients/IDE debug configurations.
8. Set a breakpoint in Orders application code and a breakpoint in Payment application code.
9. Create an order through Grove Shop.
10. Verify the Orders breakpoint is hit in the Orders worker on node-2.
11. Continue execution.
12. Verify the Payment breakpoint is hit in the Payment worker on node-4.
13. Inspect at least one application variable in each session and continue execution.
14. Verify the order completes successfully after both breakpoints are continued.
15. Disconnect both debugger sessions.
16. Verify both workers return to normal supervision and the cluster remains healthy.

## Automated acceptance
Add a deterministic Go E2E that validates everything Grove owns without requiring a GUI IDE:

- five-node placement is correct;
- Orders and Payment resolve to distinct worker processes and nodes;
- two debug sessions can coexist;
- each local DAP endpoint reaches the intended worker's Delve instance;
- DAP initialize/attach, breakpoint installation, continue, and disconnect work for both sessions;
- traffic through Grove Shop causes the expected breakpoint in each target;
- non-target workers are not paused/attached;
- cluster/debug state is observable and cleanup is complete;
- a successful order flow still completes after continuing from both breakpoints.

Use a small DAP test client from Go or a narrowly scoped test helper. Do not require GoLand/VS Code for automated acceptance.

## Manual demo guide
`demo/DEBUGGING_DEMO.md` is the human-facing walkthrough. The commands in that file are part of this task's acceptance contract and must be executed against the built implementation before this task may be marked DONE.

## Constraints
- No SSH.
- No manual PID discovery.
- No manual node discovery.
- No direct remote Delve port exposure.
- No DAP multiplexer in this task.
- No cross-service single-step semantics.
- No fixed sleeps in E2E tests.
- Preserve all tests from tasks 001-031.

## Done
- `go test ./...` passes.
- The automated two-worker DAP E2E passes.
- Every shell command documented in `demo/DEBUGGING_DEMO.md` has been run successfully against the implementation on a clean local checkout.
- A human can attach two debugger sessions to Orders and Payment using only Grove service names and local DAP endpoints, hit both breakpoints during one Grove Shop order, continue, and finish with a healthy cluster.