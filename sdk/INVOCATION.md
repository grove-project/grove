# Grove SDK Invocation Model

## Goal
Expose one explicit Grove invocation primitive that works for both local and remote execution while keeping the distributed boundary visible to application code.

## Canonical shape
The MVP should converge on an API with semantics equivalent to:

```go
resp, err := grove.Call[ReserveRequest, ReserveResponse](
    ctx,
    client,
    ServiceInventory,
    MethodReserve,
    req,
)
```

The exact generic shape may change if implementation constraints justify it, but these semantics are required:
- the call is visibly a Grove call
- service and method IDs are explicit
- request and response types are application-owned
- the same API is used whether the target is local or remote
- routing occurs underneath the invocation boundary
- errors preserve meaningful local/remote failure information

## Local path
When routing resolves the destination to the current Grovlet:

```text
application
   ↓
grove.Call
   ↓
routing
   ↓
local registry
   ↓
handler
```

No NATS round trip is required for a local call.

## Remote path
When routing resolves the destination to another Grovlet:

```text
application
   ↓
grove.Call
   ↓
encode request envelope
   ↓
System NATS request/reply
   ↓
remote Grovlet
   ↓
registry dispatch
   ↓
handler
   ↓
response envelope
```

The application call site does not switch to a generated remote client.

## Explicit destination before placement exists
Early tasks may require an explicit destination node because automatic placement/discovery has not yet been implemented. That is acceptable, but the destination should be supplied through the Grove invocation/routing layer rather than by application code opening sockets or publishing directly to NATS.

## Errors
At minimum distinguish:
- unknown service
- unknown method
- serialization failure
- destination unavailable
- transport failure/timeout
- remote handler error

Do not silently retry in the MVP unless a later task explicitly introduces retry behavior.

## Context
`context.Context` should propagate cancellation/deadline semantics through the invocation layer where practical. Do not invent a separate Grove cancellation abstraction for the MVP.