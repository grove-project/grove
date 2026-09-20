# Debug Grove Shop across two workers

Build one Grove Shop binary, start its application console, and attach ordinary
DAP clients to Orders and Payment. Grove resolves both services to their real
workers; you do not provide a PID, remote node address, or Delve port.

## Build the debug application

From the repository root:

```bash
mkdir -p bin
go build -gcflags="all=-N -l" -o ./bin/groveshop ./cmd/grovlet
```

The resulting binary contains the Grove Shop services, Grove runtime, and
operational console. It requires `dlv` on `PATH` when starting the debug demo.

## Start the five-node demo

Open the application console in Terminal C:

```bash
./bin/groveshop
```

Enter this TUI path:

```text
Deployments > Debug demo > Start
```

Grove starts one service worker on each node:

```text
Web         node-1   healthy
Orders      node-2   healthy
Inventory   node-3   healthy
Payment     node-4   healthy
Shipping    node-5   healthy
```

Inspect the same read model from another terminal:

```bash
./bin/groveshop action cluster.status
```

The JSON output includes five healthy placements. Orders reports `node-2` and
`orders-1`; Payment reports `node-4` and `payment-1`.

## Attach Orders

The contextual TUI operation is:

```text
Services > Orders > Instances > node-2 > Debug > Attach
```

For a deterministic IDE endpoint, run the same underlying action in Terminal A:

```bash
./bin/groveshop action debug.attach orders --listen 127.0.0.1:40000
```

It prints the resolved target before waiting for a DAP client:

```json
{"service_id":1,"service_name":"Orders","node_id":"node-2","worker_id":"orders-1","artifact_digest":"sha256:...","code_version":"...","dap_endpoint":"127.0.0.1:40000"}
```

Keep Terminal A running.

## Attach Payment

The contextual TUI operation is:

```text
Services > Payment > Instances > node-4 > Debug > Attach
```

Run the structured action in Terminal B:

```bash
./bin/groveshop action debug.attach payment --listen 127.0.0.1:40001
```

It independently resolves Payment:

```json
{"service_id":3,"service_name":"Payment","node_id":"node-4","worker_id":"payment-1","artifact_digest":"sha256:...","code_version":"...","dap_endpoint":"127.0.0.1:40001"}
```

Keep Terminal B running.

## Debug one order

Connect two ordinary Go/DAP attach configurations:

```text
Orders     127.0.0.1:40000
Payment    127.0.0.1:40001
```

The DAP clients use attach mode without a process ID. Grove inserts the
resolved node-local PID before forwarding each request to Delve.

Set these breakpoints in `demo/groveshop/groveshop.go`:

```go
chargeRequest := ChargeRequest{
```

```go
if req.AmountCents <= 0 {
```

Create an order through the Grove Shop Web URL shown by the debug-demo start
result. The observable sequence is:

```text
Orders/node-2     breakpoint -> inspect order.ID -> continue
Payment/node-4    breakpoint -> inspect req.OrderID -> continue
Order             completed
```

Orders and Payment are independent Delve sessions. Web, Inventory, and Shipping
remain healthy while the two target workers report `debugging`.

## Disconnect and verify health

Disconnect both DAP clients. Each `debug.attach` action exits after its client
disconnects and releases its listener, tunnel, and Delve process.

```bash
./bin/groveshop action cluster.status
```

The final result reports `healthy`, with all five components back in `healthy`
state. Enter `q` in Terminal C to stop the console and all five child Grovlets.

## Relationship to the lifecycle demo

This is a focused debugging proof built on the same Grove Shop application/runtime model. It must not redefine cluster bootstrap, artifact rollout, configuration deployment, or ingress ownership.

When the broader lifecycle demo is running, application identity/artifact identity semantics, same-artifact join behavior, different-artifact rollout behavior, shared Cluster TUI state, and stable Grove-managed ingress remain authoritative as documented in `DEMO_FLOW.md` and `IMPLEMENTATION_GUIDE.md`.

The explicit debug actions below exist to expose deterministic local DAP endpoints for IDE/acceptance use; they are not a second deployment or cluster-management CLI.

## Boundaries

Grove exposes one local DAP endpoint per selected worker. It does not multiplex
debugger state, fan out breakpoints, or provide cross-service single-step
semantics. The local application action is the only exposed debugger listener;
node-local Delve ports are not published as cluster endpoints.
