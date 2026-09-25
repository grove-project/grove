# Debugging distributed Grove services

Status: Initial MVP implemented

Grove Shop can attach ordinary Delve/DAP clients to application workers without
requiring a separate Grove CLI, SSH, PID discovery, or exposed remote Delve
ports.

## Select the service in the application console

Build a debug-capable application and open its console:

```bash
git clone https://github.com/grove-project/groveshop && cd groveshop
go build -gcflags="all=-N -l" -o ./bin/groveshop ./cmd/groveshop
./bin/groveshop
```

Start the five-node debug demo:

```text
Deployments > Debug demo > Start
```

Then select either worker in context:

```text
Services > Orders > Instances > node-2 > Debug > Attach
Services > Payment > Instances > node-4 > Debug > Attach
```

Grove resolves the current authoritative placement and worker generation. The
developer supplies only a service identity.

## Expose deterministic DAP endpoints

Automation and reproducible IDE setup use structured actions from that same
application binary:

```bash
./bin/groveshop action debug.attach orders --listen 127.0.0.1:40000
./bin/groveshop action debug.attach payment --listen 127.0.0.1:40001
```

Each long-running action prints its resolved target before accepting one DAP
client:

```json
{"service_id":1,"service_name":"Orders","node_id":"node-2","worker_id":"orders-1","artifact_digest":"sha256:...","code_version":"v0.1.0-dev","dap_endpoint":"127.0.0.1:40000"}
```

The IDE sends an ordinary DAP attach request without a PID. Grove inserts the
resolved worker PID, tunnels the DAP byte stream through System NATS, and leaves
debugging semantics to Delve.

```text
IDE/DAP client
    |
    | localhost:40000
    v
Grove Shop action
    |
    | System NATS debug stream
    v
node-2 -> Delve -> Orders worker
```

## Observe and end the session

While attached, the selected component reports `debugging`. Other workers stay
healthy and are not implicitly paused or attached.

```bash
./bin/groveshop action cluster.status
```

Disconnecting the DAP client ends the action and releases the local listener,
tunnel, and Delve process. Grove returns that exact worker generation to normal
health supervision. If the worker exits or restarts, the session closes with an
actionable error instead of attaching to a replacement silently.

See [the complete Grove Shop walkthrough](../../demo/DEBUGGING_DEMO.md) for the
two-breakpoint order flow.

## MVP boundaries

The MVP provides one independent Delve session per selected worker. It does not
multiplex debugger state, fan out breakpoints, rewrite DAP identities, or offer
cross-service single-step behavior.

Production authorization, audit policy, replicated-service selection, and edge
debugging remain future work. Node-local Delve endpoints are intentionally not
published as cluster ports.
