# Grove Shop Delve Debugging Demo

## Purpose
This is the human-facing walkthrough for Task 032. It demonstrates that Grove can resolve application services to their real distributed workers and expose ordinary Delve/DAP debugging locally without SSH, PID discovery, node discovery, or remote debugger ports.

This guide is a **Task 032 acceptance artifact**. Do not mark Task 032 DONE until every command below has been executed successfully against a clean local checkout.

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

## 1. Build the CLI and debug-capable Grove Shop artifact

```bash
go build -o ./bin/grove ./cmd/grove
go build -gcflags="all=-N -l" -o ./bin/grove-shop ./cmd/grovlet
```

Task 032 may replace the second command with a dedicated Grove artifact-build command if that becomes the implemented artifact workflow. The final accepted guide must contain the exact command that actually works.

## 2. Deploy the debug demo

The final Task 032 implementation must expose one minimal command that starts/uses the local five-node cluster, applies the dedicated demo placement, and deploys the debug-capable Grove Shop artifact. The intended UX is:

```bash
./bin/grove deploy --config configs/acme.yaml --debug-demo
```

After implementation, this section must be updated to the exact tested command if the final CLI differs.

## 3. Verify placement before debugging

```bash
./bin/grove status
./bin/grove components
```

Confirm that the structured output shows:

- Web on node-1;
- Orders on node-2;
- Inventory on node-3;
- Payment on node-4;
- Shipping on node-5;
- all five workers healthy.

The human operator must not need to copy a PID or node address into the debugger commands below.

## 4. Start the Orders debugger tunnel

Terminal A:

```bash
./bin/grove debug --service orders --listen 127.0.0.1:40000
```

The command should print the resolved Orders service, node, worker identity, artifact/version, and:

```text
DAP listening locally on 127.0.0.1:40000
```

Keep this command running.

## 5. Start the Payment debugger tunnel

Terminal B:

```bash
./bin/grove debug --service payment --listen 127.0.0.1:40001
```

The command should resolve Payment independently and print:

```text
DAP listening locally on 127.0.0.1:40001
```

Keep this command running too.

## 6. Attach two IDE debugger sessions

Create two ordinary Go/DAP attach configurations:

- Orders -> `127.0.0.1:40000`
- Payment -> `127.0.0.1:40001`

The IDE is speaking DAP to Delve through Grove. There is no Grove-specific IDE plugin requirement for MVP.

Set one breakpoint in the Orders request/order-flow path and one in the Payment charge path.

## 7. Trigger one order

Use the Grove Shop Web UI opened by the deployment command and create an order.

Expected sequence:

1. Orders breakpoint hits in the Orders worker on node-2.
2. Inspect an application variable and continue.
3. Payment breakpoint hits in the Payment worker on node-4.
4. Inspect an application variable and continue.
5. The order completes successfully.

The two debugger sessions remain independent; Grove is not multiplexing them into one synthetic debugger.

## 8. Disconnect and verify health

Disconnect both IDE sessions, then stop the two `grove debug` commands if they have not exited automatically.

Run:

```bash
./bin/grove status
./bin/grove components
```

All five Grove Shop services must be back under normal supervision and healthy.

## Acceptance rule
The commands in this guide are not documentation-only examples. Task 032 is incomplete until the implementation agent has:

- run them on a clean checkout;
- proven both local DAP endpoints target the intended remote workers;
- hit both breakpoints during one order flow;
- continued the order to completion;
- disconnected both sessions;
- rerun status/health checks successfully;
- run `go test ./...` successfully.

If any command changes during implementation, update this guide first and retest the exact final text.