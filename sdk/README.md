# Grove SDK

**Write ordinary Go. Make distribution explicit. Let Grove own the runtime mechanics.**

A Grove service starts as a normal Go type:

```go
type Inventory struct{}

func (s *Inventory) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
    if req.Qty <= 0 {
        return ReserveResponse{}, errors.New("qty must be positive")
    }
    return ReserveResponse{Reserved: true}, nil
}
```

No Grove interface. No generated base class. The business package stays directly unit-testable.

## Make a method remotely invokable

Grove makes the distributed boundary visible through stable service and method IDs plus explicit registration.

```go
const (
    ServiceInventory grove.ServiceID = 2
    MethodReserve    grove.MethodID  = 1
)

reg.Register(ServiceInventory, MethodReserve, reserveHandler)
```

The registration glue owns serialization and dispatch. It is intentionally visible and debuggable rather than generated behind the build.

## Call another service

```go
reserve, err := grove.Call[ReserveRequest, ReserveResponse](
    ctx,
    client,
    ServiceInventory,
    MethodReserve,
    ReserveRequest{OrderID: order.ID, SKU: order.SKU, Qty: 1},
)
```

The code tells you that the call may cross a distribution boundary. The source does not change when Grove moves Inventory to another node.

```text
same node    grove.Call → local registry → Inventory.Reserve
remote       grove.Call → transport      → remote registry → Inventory.Reserve
```

## Test the business logic normally

```go
func TestReserve(t *testing.T) {
    svc := &Inventory{}
    got, err := svc.Reserve(context.Background(), ReserveRequest{Qty: 1})

    require.NoError(t, err)
    require.True(t, got.Reserved)
}
```

Grove integration tests separately prove registration, routing, transport, and failure behavior.

## The SDK contract

| Topic | Read |
|---|---|
| End-to-end example | [Grove Shop](EXAMPLE.md) |
| Service registration and dispatch | [Service model](SERVICE_MODEL.md) |
| Local and remote calls | [Invocation](INVOCATION.md) |
| Request/response encoding | [Serialization](SERIALIZATION.md) |
| Constraints and non-goals | [Design principles](DESIGN_PRINCIPLES.md) |

### Design invariants

- Business services remain ordinary Go types.
- No required service interfaces.
- No generated RPC stubs or build-time code generation.
- No reflection-driven registration magic.
- Distribution is explicit at the call boundary.
- Local and remote invocation use the same Grove call API.
- Normal IDE navigation reaches concrete implementations.
- Business logic remains testable without starting Grove.

> **MVP status:** this directory defines the intended SDK contract. Exact package names may evolve while the implementation catches up.