# Grove SDK Service Model

## Stable identifiers
Every remotely invokable service and method has an application-owned stable ID.

```go
const (
    ServiceOrders    grove.ServiceID = 1
    ServiceInventory grove.ServiceID = 2
    ServicePayment   grove.ServiceID = 3
    ServiceShipping  grove.ServiceID = 4
)

const (
    MethodReserve grove.MethodID = 1
)
```

IDs must not be derived from package names, reflection metadata, function addresses, or declaration order.

## Registration
The application explicitly registers handlers with Grove.

Canonical MVP shape:

```go
registry.Register(
    ServiceInventory,
    MethodReserve,
    func(ctx context.Context, payload []byte) ([]byte, error) {
        var req ReserveRequest
        if err := grove.Decode(payload, &req); err != nil {
            return nil, err
        }

        resp, err := inventory.Reserve(ctx, req)
        if err != nil {
            return nil, err
        }

        return grove.Encode(resp)
    },
)
```

The exact package/type names may evolve during implementation, but the semantics are fixed:
- explicit service ID
- explicit method ID
- explicit concrete implementation
- explicit decode/call/encode boundary
- no reflection-based method lookup
- no generated adapter required

## Registry behavior
The registry must:
- reject duplicate service/method registrations
- distinguish unknown service from unknown method
- dispatch deterministically by `(ServiceID, MethodID)`
- remain independent of network transport
- support local dispatch without serialization transport concerns leaking into business code

## Service ownership
Registration says **what this process can execute**. Placement and routing say **where a call should execute**. Do not conflate the local method registry with cluster-wide placement state.

## Placement validation
A Grove service may optionally provide developer-defined placement validation through the SDK. The validator answers one question on a specific Grovlet: **can this service run correctly on this node?**

No placement validator means the service is eligible to run on every Grove node.

The validator is evaluated locally by each Grovlet because eligibility may depend on node-local environmental truth that the control plane cannot reliably infer from static metadata. Examples include reachability to a customer-LAN endpoint, presence of a local Unix socket or device, access to a site-local service, or another runtime capability required by the service.

Conceptual shape:

```go
grove.Service(
    ServiceEdgeAdapter,
    edgeAdapter.Run,
    grove.PlacementValidator(func(ctx context.Context) error {
        return requireTCPReachable(ctx, "192.168.10.20:443")
    }),
)
```

The exact API shape may evolve, but the semantics are fixed:
- placement validation is optional;
- absent validator = eligible everywhere;
- success = this Grovlet is eligible to host the service;
- failure = this Grovlet must not host the service;
- validation runs on the candidate Grovlet, not centrally;
- validation should be read-only, bounded, safe to repeat, and should not perform environment setup;
- eligibility is a hard constraint, not a scheduling preference.

Placement validation and scheduling are separate concepts. Validation determines the eligible node set. Scheduling chooses among eligible nodes according to locality, capacity, affinity, or other policy.

A service must never be started on a Grovlet whose placement validation does not pass.

## Business types
Grove does not own application request/response types. They remain normal Go structs in the application package.

```go
type ReserveRequest struct {
    OrderID string
    SKU     string
    Qty     int
}

type ReserveResponse struct {
    Reserved bool
}
```

## Failure behavior
Handler failures are returned as explicit errors and later encoded into Grove's response envelope. A handler panic may be treated as component failure by the runtime, but the registry itself must not invent recovery semantics.
