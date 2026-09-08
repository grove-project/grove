# Grove SDK

Grove keeps business logic ordinary Go and makes distributed boundaries explicit.

```go
type Inventory struct{}

func (s *Inventory) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
    return ReserveResponse{Reserved: true}, nil
}
```

Register methods that Grove may invoke remotely, then call them through the explicit Grove boundary:

```go
result, err := grove.Call[ReserveRequest, ReserveResponse](
    ctx,
    client,
    ServiceInventory,
    MethodReserve,
    req,
)
```

The important property is visible in the source: **this call may cross a distribution boundary.** There are no generated RPC stubs and Grove does not pretend a remote call is an ordinary local call.

## What the SDK optimizes for

- ordinary Go business types and methods
- explicit service and method registration
- explicit distributed calls
- direct unit testing without starting Grove
- normal IDE navigation to concrete implementations
- the same application code whether a target is local or remote

## Go deeper

- [Canonical Grove Shop example](../../sdk/EXAMPLE.md)
- [Service model](../../sdk/SERVICE_MODEL.md)
- [Invocation contract](../../sdk/INVOCATION.md)
- [Serialization](../../sdk/SERIALIZATION.md)
- [Design principles](../../sdk/DESIGN_PRINCIPLES.md)
- [Testing](testing.md)
- [Capabilities and trade-offs](capabilities.md)

The files under top-level [`sdk/`](../../sdk/) are the normative implementation contract. This section explains the experience that contract is intended to produce.