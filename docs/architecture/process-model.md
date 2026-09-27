# Process Model

Placement answers **where** a service runs. Execution answers **inside which process** it runs. Grove decides them separately.

By default every service placed on a node runs in that node's single **application runtime** process, next to the Grovlet that supervises it:

```text
node-1
  Grovlet (supervisor, System NATS, control plane)
  └─ app-runtime-1   pid 4242   Orders  Inventory  Payment  Shipping  Web
```

A dedicated **worker** process is an execution option, not the shape of a service. It is chosen only when a service is isolated explicitly:

```bash
$ ./groveshop --component-isolate payment ...
```

```text
node-1
  Grovlet
  ├─ app-runtime-1   pid 4242   Orders  Inventory  Shipping  Web
  └─ worker-1        pid 4250   Payment   (isolated)
```

This is the model in [ADR-002](../adr/002-grovlet-worker-process-boundary.md): the Grovlet never runs application code, and a node runs the fewest application processes that isolation requires.

## What you see

Component status reports the process for every service. Services sharing the runtime share its `process_id` and `pid`:

```json
{"name": "Orders",    "worker_id": "orders-1",    "execution_mode": "in-process",       "process_id": "app-runtime-1", "pid": 4242, "state": "healthy"}
{"name": "Inventory", "worker_id": "inventory-1", "execution_mode": "in-process",       "process_id": "app-runtime-1", "pid": 4242, "state": "healthy"}
{"name": "Payment",   "worker_id": "payment-1",   "execution_mode": "isolated-process", "process_id": "worker-1",      "pid": 4250, "state": "healthy"}
```

`worker_id` names the service instance generation, not a process. The TUI shows the same split: **Nodes** counts processes per node, and placements and hosted rows have a **PROCESS** column.

## Calls

Application code is unchanged; `grove.Call` still resolves the destination through placement. Only the transport depends on where the selected destination runs:

```text
grove.Call
   └─ placement selects an endpoint
        ├─ endpoint served by the caller's own process  → in-process dispatch
        └─ any other endpoint (isolated worker, other node) → System NATS
```

In-process dispatch keeps Grove's request envelope and Gob payload encoding, so local and remote calls behave the same. It skips only the network hop.

## Failure boundaries

| What fails | What is affected | What Grove does |
| --- | --- | --- |
| One service in the runtime (stopped, killed, HTTP listener lost) | That service only | Marks it failed; desired-state reconciliation restarts it in the same runtime |
| The application runtime process | Every service in it | Marks each failed; they restart together in a new runtime (`app-runtime-2`) |
| An isolated worker | Its one service | Marks it failed and restarts it in a new worker |
| The node | Everything on it | Recovery moves placements to surviving nodes |

A service's background work started from `Register` runs on a context that ends when the service stops, so stopping one service does not stop its neighbours. Work that ignores that context keeps running until the runtime exits.

## Debugging

A debugger attaches to a whole process. Debugging a service in the shared runtime pauses every service in that runtime at a breakpoint, and a second debug session for another service in the same process is refused until the first ends:

```text
select service 3 worker on node-1: component lifecycle transition is invalid:
process app-runtime-1 is already being debugged through Orders
```

Isolate a service to debug it independently of its neighbours.

## Trade-offs and what comes next

- The default runtime trades process isolation for lower overhead: a crash or deadlock in one service takes its runtime neighbours with it, but never the Grovlet.
- Isolation is decided per service today (`--component-isolate`). The runtime already routes and tracks execution per endpoint, so a future scheduler can move a single handler into a dedicated worker, or run several isolated instances, without another change to the process model. Deciding *when* to isolate (noisy-neighbour detection, profiling) is not implemented.
