ADR-009 — DAP as Grove Debugging Interface

Status  
Accepted

Context  
Grove applications may execute across multiple OS processes: the Grovlet itself, application workers, ingress, and eventually replicas on different nodes. Developers should still be able to use the normal debugger built into their IDE without manually discovering PIDs, nodes, or debugger ports.

Delve already implements the Debug Adapter Protocol (DAP), the standard protocol used by IDEs for breakpoints, stepping, stack traces, variable inspection, expression evaluation, and debugger events. Grove should build on this standard rather than introduce an IDE-specific debugging protocol.

Decision  
DAP is the external debugging interface between developer IDEs and Grove.

The IDE connects to a Grovlet-owned Grove Debug Gateway using DAP. The Grovlet uses its knowledge of application topology, service placement, processes, and replicas to route debugging operations to the appropriate Delve instance.

Conceptual architecture

IDE  
  │  
  │ DAP  
  ▼  
Grovlet Debug Gateway  
  │  
  ├── Delve → Grovlet process  
  ├── Delve → Worker A  
  ├── Delve → Worker B  
  └── Delve → other local/remote Grove processes

A breakpoint is expressed by the IDE as a source location. Grove resolves that source location to the service and process containing the code, then applies the breakpoint to the corresponding Delve target. Runtime/Grovlet source can therefore be debugged through the same logical interface as application source.

Initial implementation  
The first implementation SHOULD NOT attempt to multiplex multiple Delve sessions into one synthetic debugger.

The Grovlet acts as a service-aware discovery and tunnel layer:

1. Developer selects a Grove service/debug target.  
2. Grovlet resolves the worker and node hosting that target.  
3. Grovlet starts or attaches Delve to the process.  
4. Grovlet exposes/tunnels the Delve DAP connection to the IDE.  
5. IDE communicates with the selected Delve instance using ordinary DAP.

This provides most of the desired developer experience while keeping Grove largely unaware of DAP session internals.

Future extension: DAP multiplexing  
Grove may later implement a stateful DAP multiplexer that presents several Delve processes as one logical debugger.

The multiplexer would maintain globally unique mappings for debugger state such as thread IDs, stack-frame IDs, breakpoint IDs, and variable references and route requests/events to the owning Delve session.

This enables:  
• breakpoints spanning multiple services or replicas;  
• switching between Grove processes without manual reattachment;  
• cluster-aware process/thread views;  
• debugging across worker restarts or placement changes;  
• potentially stepping across Grove RPC/service boundaries.

Replica semantics  
A source location may exist in multiple replicas. Grove therefore must make breakpoint targeting explicit. The debugger should support selecting a specific replica and may later support applying a breakpoint to all replicas. The IDE should not need to reason about PIDs or node addresses.

Cross-service Step Into  
A future Grove DAP multiplexer may correlate Grove RPC requests with debugger execution. When execution crosses a Grove service boundary, Grove could attach/select the receiving process and continue the debugging experience there. This is an advanced capability and is explicitly not required for the initial implementation.

Consequences  
Positive:  
• Standard IDE compatibility through DAP.  
• Delve remains responsible for Go debugging semantics.  
• Grove owns only Grove-specific topology and routing knowledge.  
• Developers debug services/source rather than nodes and PIDs.  
• Remote and distributed debugging can evolve without changing the developer-facing protocol.  
• The initial implementation can remain small by tunneling a single Delve DAP session.

Costs and risks:  
• True multi-process debugging requires a stateful DAP multiplexer.  
• IDs and debugger state from multiple Delve sessions must be namespaced.  
• Replica breakpoint semantics require explicit policy.  
• Process restart/migration and cross-service stepping are substantially more complex than basic attachment.

Guiding principle  
Grove should make distributed debugging feel like ordinary Go debugging. IDEs speak DAP, Delve debugs Go processes, and Grove supplies the missing topology awareness between them.  
