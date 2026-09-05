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