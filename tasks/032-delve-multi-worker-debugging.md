# Task 032 — Delve debugging across Grove Shop workers

Status: DONE
Depends on: 031

## Goal
Add the final MVP capability: attach ordinary Delve/DAP debugger sessions to Grove Shop services through Grove, without manually discovering worker PIDs, node addresses, or Delve ports.

The acceptance demo must attach debuggers to **two different Grove Shop workers running on two different Grovlets** and debug real Grove Shop application code.

The operational surface must be provided by the Grove Shop application binary itself. Do not require a separately installed `grove` CLI for the human or scripted workflow.

## Required reading
- `docs/adr/009-dap-debugging-interface.md`
- `docs/developer-experience/debugging.md`
- `docs/cli/README.md`
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
6. The human target-selection flow is available from the Grove Shop TUI.
7. The same debugger attach capability is exposed as a structured action from the Grove Shop binary for automation and deterministic tests.

Do **not** implement a synthetic multi-process DAP multiplexer, cross-service instruction-level step-into, or debugger-state ID rewriting. The native TUI may coordinate semantic Grove breakpoint intent across services while Delve remains the debugging engine.

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

## Application console contract
The human workflow should be service-oriented and contextual:

```text
Services > Orders > Instances > node-2 > Debug > Attach
Services > Payment > Instances > node-4 > Debug > Attach
```

The user must not provide PIDs, remote node addresses, or remote Delve ports.

For automation and deterministic local DAP endpoints, the same Grove Shop binary exposes structured actions:

```bash
./bin/groveshop action debug.attach orders --listen 127.0.0.1:40000
./bin/groveshop action debug.attach payment --listen 127.0.0.1:40001
```

Each attach action:
- resolves the current authoritative service instance;
- prints the resolved service, node, worker identity, artifact/version, and local DAP endpoint;
- starts/attaches Delve on the target node;
- keeps the DAP tunnel alive until the client disconnects or the action is interrupted;
- cleans up debugger resources on exit;
- fails with actionable diagnostics if the service is not running, has multiple ambiguous targets, Delve cannot start, or the worker exits/restarts.

The TUI must use the same underlying action/operation rather than implementing a separate debugging path.

## Debugger behavior
- Use Delve's DAP mode; Grove must not implement Go debugging semantics itself.
- Build/run the debug-demo artifact with compiler optimizations and inlining disabled where required for reliable source breakpoints (`-gcflags="all=-N -l"` or equivalent artifact-build support).
- Debugging one worker must not pause or attach to another worker implicitly.
- Health/recovery behavior while a worker is stopped at a breakpoint must be deterministic for the demo. The MVP may mark a worker as explicitly `debugging` and suppress normal failure/recovery reactions for that worker while an authorized debug session is active.
- Ending the debug session returns the worker to normal health supervision.
- DAP listeners used by Delve should not be exposed directly as public cluster ports; the developer connects to the local Grove endpoint/tunnel.

## Required demo scenario
1. Build one debug-capable Grove Shop application binary containing the Grove runtime and console.
2. Start the five-node local Grove cluster from the Grove Shop experience.
3. Deploy Grove Shop with one service per node as defined above.
4. Verify all five services are healthy in the TUI and record the Orders and Payment worker/node identities.
5. Start a Grove debug session for Orders and expose local DAP endpoint `127.0.0.1:40000` using the Grove Shop binary.
6. Start a second Grove debug session for Payment and expose local DAP endpoint `127.0.0.1:40001` using the same binary.
7. Connect two independent DAP clients/IDE debug configurations.
8. Set a breakpoint in Orders application code and a breakpoint in Payment application code.
9. Create an order through Grove Shop.
10. Verify the Orders breakpoint is hit in the Orders worker on node-2.
11. Continue execution.
12. Verify the Payment breakpoint is hit in the Payment worker on node-4.
13. Inspect at least one application variable in each session and continue execution.
14. Verify the order completes successfully after both breakpoints are continued.
15. Disconnect both debugger sessions.
16. Verify both workers return to normal supervision and the cluster remains healthy in the TUI/read model.

## Native TUI screen contracts

The native Grove Shop debugger is the primary human experience. External DAP endpoints remain supported, but the TUI must expose the same underlying Grove debug manager and Delve state.

### 1. Observed flow / breakpoint suggestions

After an order executes, `Debug > Last flow` shows the registered Grove operations that actually participated in that request. Grove already knows these semantic boundaries from service registration and `grove.Call`; do not infer the primary application flow from raw profiling.

```text
 GROVE / DEBUG / LAST FLOW                         POST /orders · 184ms
──────────────────────────────────────────────────────────────────────────────

 EXECUTED GROVE CALLS                         SUGGESTED BREAKPOINTS
───────────────────────────────────────┬──────────────────────────────────────
 web.PlaceOrder             node-1     │ [ ] web.PlaceOrder
       │                               │ [◆] orders.CreateOrder
       ▼                               │ [◆] inventory.Reserve
 orders.CreateOrder         node-2     │ [◆] payments.Charge
       │                               │
       ├──── inventory.Reserve node-1  │
       │                               │
       └──── payments.Charge   node-2  │
───────────────────────────────────────┴──────────────────────────────────────
 j/k navigate   Enter source   Space breakpoint   a recommended   t trace
```

Node identities above are illustrative. The screen must always render current placement.

`Enter` on a registered operation opens its embedded source. `Space` toggles a semantic Grove breakpoint identified by service/method identity rather than a hard-coded node or source line.

### 2. Source navigation

Source is the dominant debugging pane. Navigation is keyboard-first and organized around application semantics rather than directory traversal.

```text
 GROVE / DEBUG / SOURCE                         orders · node-2 · worker-7
──────────────────────────────────────────────────────────────────────────────

 FILES / SYMBOLS              internal/orders/service.go
─────────────────────┬────────────────────────────────────────────────────────
 orders              │  39   order := newOrder(req)
   service.go        │  40
   repository.go     │◆ 41   if err := s.reserve(ctx, order); err != nil {
                     │  42       return nil, err
 payments            │  43   }
   service.go        │  44
   gateway.go        │▶ 45   receipt, err := s.charge(ctx, order)
                     │  46   if err != nil {
 inventory           │  47       return nil, err
   service.go        │  48   }
─────────────────────┴────────────────────────────────────────────────────────
 orders.CreateOrder · service.go:45 · BP:1
──────────────────────────────────────────────────────────────────────────────
 j/k move  Space breakpoint  Enter follow  / search  Ctrl-P source  @ symbols
```

Required navigation:
- `Ctrl-P`: fuzzy search files, packages, services, types, functions, and methods;
- `@`: symbols in the current file;
- `/`, `n`, `N`: text search and next/previous result;
- `:<line>`: go to line;
- `gg` / `G`: file top/bottom;
- `Enter`: follow a resolvable symbol or registered Grove call destination;
- `Alt-Left` / `Alt-Right`: source-navigation history;
- `Space`: toggle a source/Grove breakpoint as appropriate.

A selected `grove.Call` should be able to navigate directly to the registered destination implementation instead of forcing the user through Grove transport plumbing.

### 3. Breakpoint list

```text
 GROVE / DEBUG / BREAKPOINTS
──────────────────────────────────────────────────────────────────────────────

 ◆ orders.CreateOrder       service.go:41     all instances
 ◆ payments.Charge          service.go:91     all instances
 ● gateway.go:143           payments          source breakpoint

──────────────────────────────────────────────────────────────────────────────
 j/k select   Enter source   Space enable/disable   x remove   Esc back
```

`◆` denotes a semantic Grove breakpoint. `●` denotes an arbitrary source breakpoint.

### 4. Paused source + locals

When a breakpoint hits, the TUI automatically switches to the paused debugger. Source remains dominant and Locals becomes the default contextual pane.

```text
 GROVE / DEBUG                    PAUSED ●  payments / node-2 / worker-7
──────────────────────────────────────────────────────────────────────────────

 service.go                                     LOCALS
─────────────────────────────────────────┬────────────────────────────────────
  88 func (s *Service) Charge(           │ req *ChargeRequest
  89     ctx context.Context,             │ ├─ OrderID    "O-184"
  90     req ChargeRequest,               │ ├─ Amount     149.00
▶ 91 ) error {                            │ └─ Currency   "USD"
  92     payment := newPayment(req)       │
  93                                      │ payment *Payment
  94     err := s.gateway.Charge(...)     │ ├─ Status     "pending"
                                          │ └─ ...
─────────────────────────────────────────┴────────────────────────────────────
 payments.Charge · service.go:91 · goroutine 231
──────────────────────────────────────────────────────────────────────────────
 F5 continue  F10 over  F11 into  ⇧F11 out  v locals  s stack  g goroutines
```

Pressing `v` focuses Locals. `j/k` moves between values and `Enter` expands/collapses structs, pointers, slices, maps, and nested values.

### 5. Stack context

```text
 CONTEXT / STACK
────────────────────────────────────────
 > payments.Charge          service.go:91
   payments.Handle          handler.go:72
   grove.rpc.invoke         rpc.go:182
   grove.worker.run         worker.go:94
────────────────────────────────────────
 j/k frame   Enter/open   v locals
```

Changing the selected frame updates both the source pane and locals for that frame.

### 6. Goroutines

```text
 CONTEXT / GOROUTINES
────────────────────────────────────────
 > 231  stopped   payments.Charge
   114  waiting   nats.(*Conn).readLoop
    88  waiting   runtime.gopark
────────────────────────────────────────
 j/k select   Enter stack
```

### 7. Expression evaluation

```text
┌─ Evaluate ──────────────────────────────────────────────┐
│ > req.Amount * 1.17                                    │
│                                                       │
│ 174.33                                                │
└───────────────────────────────────────────────────────┘
```

`e` opens evaluation while paused. Evaluation is backed by Delve; Grove must not implement Go expression semantics.

### 8. Trace/flow ↔ source navigation

The contextual `t` view preserves the runtime path that led to the debugging session:

```text
 TRACE / OBSERVED FLOW
────────────────────────────────────────────────────────
 POST /orders
 │
 ├─ web.PlaceOrder              node-1
 ├─ orders.CreateOrder          node-2
 ├─ inventory.Reserve           node-1
 └─ payments.Charge             node-2   ← current
────────────────────────────────────────────────────────
 j/k select   Enter source   Space breakpoint
```

Selecting an operation and pressing `Enter` opens its source. This navigation must work in both directions: observed flow -> source and paused source -> flow.

### 9. Cross-node debugging proof

The demo must visibly prove that node placement is not part of the debugging workflow:

```text
orders.CreateOrder
node-1 / worker-3
      │
      │ F5 continue
      ▼
payments.Charge
node-2 / worker-7
```

The TUI changes source, locals, stack, worker, and node context automatically. The user does not reconnect, choose another Delve port, discover a PID, or open a second native debugger.

### Keyboard contract summary

```text
j/k          navigate
h/l          pane / collapse / expand
Enter        open / follow / expand
Space        toggle breakpoint
Ctrl-P       source/symbol search
@            current-file symbols
/            source search
b            breakpoints
t            trace / observed flow
s            stack
v            locals
g            goroutines
e            evaluate
F5           continue
F10          step over
F11          step into
Shift-F11    step out
Alt-Left     navigation back
Alt-Right    navigation forward
```

These screens are behavioral contracts, not pixel-perfect layouts. Implementations may adapt dimensions to terminal size, but the information hierarchy, keyboard-first workflow, and source/flow/debug relationships must remain recognizable.

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
- a successful order flow still completes after continuing from both breakpoints;
- the TUI operation and structured action share the same underlying debug-attach implementation.

Use a small DAP test client from Go or a narrowly scoped test helper. Do not require GoLand/VS Code for automated acceptance.

## Manual demo guide
`demo/DEBUGGING_DEMO.md` is the human-facing walkthrough. The TUI paths and application-binary actions in that file are part of this task's acceptance contract and must be executed against the built implementation before this task may be marked DONE.

## Constraints
- No separate required `grove` CLI executable.
- No SSH.
- No manual PID discovery.
- No manual node discovery.
- No direct remote Delve port exposure.
- No DAP multiplexer in this task.
- No synthetic cross-service instruction-level single-step semantics.
- No fixed sleeps in E2E tests.
- Preserve all tests from tasks 001-031.

## Done
- `go test ./...` passes.
- The automated two-worker DAP E2E passes.
- Every shell action and TUI path documented in `demo/DEBUGGING_DEMO.md` has been run successfully against the implementation on a clean local checkout.
- A human can attach two debugger sessions to Orders and Payment from the Grove Shop console using only service identity and local DAP endpoints, hit both breakpoints during one Grove Shop order, continue, and finish with a healthy cluster.
