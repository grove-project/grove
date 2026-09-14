Debugging

Status: Initial MVP implemented

MVP Experience

```bash
grove debug --service orders --listen 127.0.0.1:40000
grove debug --service payment --listen 127.0.0.1:40001
```

Each command resolves authoritative placement, selects the current worker,
starts node-local Delve in DAP mode, and exposes only the requested local
endpoint. The client sends ordinary DAP. It never supplies a remote node, PID,
or Delve port.

```text
Service  orders (1)
Node     node-2
Worker   orders-1
Artifact sha256:...
Version  0.0.0-dev
DAP listening locally on 127.0.0.1:40000
```

The MVP supports independent single-worker sessions. It does not multiplex
debugger state, fan breakpoints across replicas, or step across services.

Operational Behavior

While attached, the selected worker reports `debugging`. Grove still monitors
the process but treats an intentional breakpoint pause as healthy. Disconnect
returns that exact worker generation to `healthy`; a worker exit still becomes
`failed` and closes the session.

Goal  
Make debugging a Grove-native operational capability rather than a separate deployment-specific workflow.

Core Direction  
Grove should integrate with Delve so debugging can be enabled or attached at runtime when permitted. The application console should help identify the correct service, version, worker, replica, and node before attaching.

Expected Workflow  
• Start the application binary's TUI.  
• Select a service or specific replica.  
• Inspect the worker, node, version, health, and recent failures in context.  
• Choose Debug / Attach.  
• Let Grove expose a local DAP endpoint and route the session to the correct worker.  
• Debug using cluster-aware context rather than manually locating a process.  
• Stop the debugging session and return the worker to normal operation.

Conceptual TUI flow:

```text
Services > orders > Instances > node-2 > Debug

Target       orders / node-2
Worker       worker-17
Version      v0.8.2
Health       healthy

[Attach debugger]
```

After attach:

```text
DAP endpoint
  127.0.0.1:4711

Waiting for IDE...
```

For automation and reproducible demos, the same capability is available as a structured action from the application binary:

```bash
./groveshop action debug.attach orders --listen 127.0.0.1:4711
```

Production Debugging  
Remote debugging can be extremely valuable for support and customer environments, but it is privileged. Grove must require explicit authorization and should make every production debugging action auditable.

Desired Console Directions  
• TUI-first service and replica selection.  
• Contextual Debug action from the selected target.  
• Runtime start/stop of debugging where technically possible.  
• Capture surrounding logs, health, version, placement, and recent failures in the same view.  
• Structured non-interactive `debug.attach` action for automation and IDE setup.

Open Questions  
• Authentication and authorization model.  
• Delve transport and exposure model.  
• How pausing a goroutine/process affects health checks and failover.  
• Safe behavior for replicated services while one replica is being debugged.

Application-First Troubleshooting  
Grove should present failures in application vocabulary before infrastructure vocabulary. A developer should be able to start from a service or business flow and progressively reveal workers, nodes, routing, storage, and control-plane details.

A service's Diagnose view should correlate recent restarts, dependency failures, placement changes, routing events, storage transitions, logs, and traces into a concise explanation with suggested next inspection steps.

Hide Mechanisms, Never State  
Automation should not make the runtime opaque. Developers must be able to answer where a service is running, why Grove placed it there, which dependencies it is using, where its data is located, and what changed around a failure.

Debugging UX Principle  
The developer should not need to manually discover a node, SSH into it, find a PID, forward a port, and then attach Delve. Grove already knows the service, version, worker, node, and surrounding runtime state and should use that context to make debugging direct.

Edge Debugging and Diagnostics  
Edge nodes must provide the same Grove troubleshooting and debugging model as cloud nodes. An edge deployment is not a separate operational product: Grove should expose its topology, service state, version, logs, traces, storage health, connectivity, and debugger targets through the same application console.

Customer edge networks should not need to expose SSH, Delve/DAP, Grove, or other inbound Internet ports. The edge Grovlet initiates the trusted outbound connection to the cloud-side Grove fabric, and diagnostics/debugging sessions are routed back through that existing relationship subject to authorization and audit policy.

Edge status should make network-boundary problems obvious. Operators should be able to distinguish a healthy autonomous edge whose cloud connection is temporarily unavailable from a failed edge runtime. Useful status includes connection state and duration, latency, last heartbeat, current/desired version, local service health, queued work, storage health, and autonomous/disconnected mode.

The debugging UX principle therefore applies regardless of placement: the operator selects the application/service in the TUI; Grove resolves whether it is local, cloud, or customer edge and routes the diagnostic or DAP session to the correct process without requiring manual network access.  
