# Grove Shop Delve Debugging Demo

## Purpose
This is the human-facing walkthrough for Task 032. It demonstrates that Grove can resolve application services to their real distributed workers and expose ordinary Delve/DAP debugging locally without SSH, PID discovery, node discovery, or remote debugger ports.

The human workflow is TUI-first and is exposed by the Grove Shop application binary itself. Structured actions from the same binary are used where the demo needs deterministic, scriptable debugger endpoints.

This guide is a **Task 032 acceptance artifact**. Do not mark Task 032 DONE until every action below has been executed successfully against a clean local checkout.

## Required topology
The debug demo runs one Grove Shop service per Grovlet:

| Node | Service |
| --- | --- |
| node-1 | Web |
| node-2 | Orders |
| node-3 | Inventory |
| node-4 | Payment |
| node-5 | Shipping |

Orders and Payment therefore live in different worker processes on different nodes.

## 1. Build the debug-capable Grove Shop application binary

```bash
go build -gcflags="all=-N -l" -o ./bin/groveshop ./demo/groveshop
```

If the implemented Grove Shop entry point differs, update this guide to the exact command that actually works. The important contract is that the produced application binary contains both the Grove runtime and the Grove operational console; no separate `grove` CLI binary is required.

## 2. Start the Grove Shop console

```bash
./bin/groveshop
```

The application opens the Grove TUI in an interactive terminal.

Navigate to the deployment/debug-demo flow and start the five-node demo topology. The intended experience is:

```text
Deployments > Debug demo > Start
```

The final implementation may refine labels, but the human should not need to construct a generic command/flag tree.

## 3. Verify placement before debugging

From the TUI:

```text
Services

Web         node-1   healthy
Orders      node-2   healthy
Inventory   node-3   healthy
Payment     node-4   healthy
Shipping    node-5   healthy
```

Confirm all five workers are healthy and that Orders and Payment are hosted on different nodes.

For automated verification, use the structured action exposed by the same application binary:

```bash
./bin/groveshop action cluster.status
```

The human operator must not need to copy a PID or remote node address into the debugger flow.

## 4. Attach the Orders debugger

Human workflow:

```text
Services > Orders > Instances > node-2 > Debug > Attach
```

For the deterministic demo endpoint used by the IDE, Terminal A runs:

```bash
./bin/groveshop action debug.attach orders --listen 127.0.0.1:40000
```

The action should print the resolved Orders service, node, worker identity, artifact/version, and:

```text
DAP listening locally on 127.0.0.1:40000
```

Keep this action running.

## 5. Attach the Payment debugger

Human workflow:

```text
Services > Payment > Instances > node-4 > Debug > Attach
```

For the deterministic demo endpoint, Terminal B runs:

```bash
./bin/groveshop action debug.attach payment --listen 127.0.0.1:40001
```

The action should resolve Payment independently and print:

```text
DAP listening locally on 127.0.0.1:40001
```

Keep this action running too.

## 6. Attach two IDE debugger sessions

Create two ordinary Go/DAP attach configurations:

- Orders -> `127.0.0.1:40000`
- Payment -> `127.0.0.1:40001`

The IDE is speaking DAP to Delve through Grove. There is no Grove-specific IDE plugin requirement for MVP.

Set one breakpoint in the Orders request/order-flow path and one in the Payment charge path.

## 7. Trigger one order

Use the Grove Shop Web UI and create an order.

Expected sequence:

1. Orders breakpoint hits in the Orders worker on node-2.
2. Inspect an application variable and continue.
3. Payment breakpoint hits in the Payment worker on node-4.
4. Inspect an application variable and continue.
5. The order completes successfully.

The two debugger sessions remain independent; Grove is not multiplexing them into one synthetic debugger.

## 8. Disconnect and verify health

Disconnect both IDE sessions, then stop the two `debug.attach` actions if they have not exited automatically.

Return to the TUI and verify:

```text
Cluster

Health      healthy
Services    5 / 5 healthy
```

For automated verification:

```bash
./bin/groveshop action cluster.status
```

All five Grove Shop services must be back under normal supervision and healthy.

## Acceptance rule
The actions in this guide are not documentation-only examples. Task 032 is incomplete until the implementation agent has:

- built one debug-capable Grove Shop application binary containing the runtime and operational console;
- opened the TUI and verified the five-node service placement;
- run the two application-binary `debug.attach` actions;
- proven both local DAP endpoints target the intended remote workers;
- hit both breakpoints during one order flow;
- continued the order to completion;
- disconnected both sessions;
- rerun cluster health checks successfully;
- run `go test ./...` successfully.

If any action or TUI path changes during implementation, update this guide first and retest the exact final text.