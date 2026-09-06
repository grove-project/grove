# Debugging

Grove should make cluster-aware debugging direct: select the application service and let Grove resolve the node, worker, process, and debugger route.

```bash
$ grove debug orders

Service     orders
Version     v0.8.2
Node        grove-2
Worker      17
Debugger    ready
DAP         connected
```

> The command above describes the intended Grove experience while debugger integration is still evolving.

The user should not need to manually SSH to a node, find a PID, forward a port, and attach Delve. Grove already owns the placement context.

## Before attaching

Debugging should expose enough surrounding state to make the session meaningful:

```bash
$ grove inspect orders

Health       degraded
Node         grove-2
Restarts     3
Last change  config production-43
Recent error invalid batch size
```

Then attach to the same service through Grove.

## Production and edge

Remote debugging is privileged and should require explicit authorization and auditability.

The same workflow should work for cloud and customer-edge services. Edge nodes should not need inbound Delve, DAP, SSH, or Grove ports; the debugger session should route through the trusted Grove connectivity already initiated by the edge Grovlet.

## Principle

> **Select the application service. Grove resolves the infrastructure.**

Automation may hide mechanics, but never the state needed to understand what is being debugged.