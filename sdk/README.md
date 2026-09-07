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

## Add persistence where state matters

Persistent application state is a runtime capability rather than infrastructure the application team has to assemble around the service.

```go
order, err := s.orders.Get(ctx, orderID)
if err != nil {
    return err
}

order.Status = "processing"
if err := s.orders.Put(ctx, order.ID, order); err != nil {
    return err
}
```

The service can restart or move. The application state does not disappear with the process.

## Add durable execution where progress matters

Use named durable steps when a logical operation must survive process failure, node failure, restart, or relocation:

```go
payment, err := grove.Step(
    ctx,
    "charge",
    func(ctx grove.Context) (Payment, error) {
        return payments.Charge(
            ctx,
            order.Customer,
            order.Amount,
            grove.IdempotencyKey(ctx),
        )
    },
)
```

Grove durably records successful step completion together with the step result. On recovery, completed steps return their persisted results without re-running; incomplete steps are retried.

The contract is deliberately explicit:

- durable step execution is **at least once**
- completed step results are durably persisted
- durable step results must be **Gob-serializable**
- external side effects should use the stable `grove.IdempotencyKey(ctx)` supplied for the logical step

This gives the developer a small programming primitive while Grove owns replay, recovery, retry identity, durable result storage, and execution migration.

→ **[Durable execution and persistent state](DURABLE_EXECUTION.md)** — full order-flow example, retry semantics, idempotency, Gob result contract, and CLI visibility.

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

Grove integration tests separately prove registration, routing, transport, durability, recovery, and failure behavior.

## The SDK contract

| Topic | Read |
|---|---|
| End-to-end example | [Grove Shop](EXAMPLE.md) |
| Persistent state and durable execution | [Durable execution](DURABLE_EXECUTION.md) |
| Service registration and dispatch | [Service model](SERVICE_MODEL.md) |
| Local and remote calls | [Invocation](INVOCATION.md) |
| Request/response and durable-result encoding | [Serialization](SERIALIZATION.md) |
| Constraints and non-goals | [Design principles](DESIGN_PRINCIPLES.md) |

### Design invariants

- Business services remain ordinary Go types.
- No required service interfaces.
- No generated RPC stubs or build-time code generation.
- No reflection-driven registration magic.
- Distribution is explicit at the call boundary.
- Persistence and durable execution are explicit at state and execution boundaries.
- Durable boundaries are serialization boundaries.
- Local and remote invocation use the same Grove call API.
- Normal IDE navigation reaches concrete implementations.
- Business logic remains testable without starting Grove.
- SDK semantics should automatically become observable and operable through Grove CLI tooling.

> **MVP status:** this directory defines the intended SDK contract. Exact package names may evolve while the implementation catches up.