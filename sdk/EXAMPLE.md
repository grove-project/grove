# Grove SDK Canonical Example — Grove Shop

This example is normative for the MVP developer experience. Exact package names may move, but the shape should remain recognizable.

## Business implementation

```go
type Inventory struct{}

type ReserveRequest struct {
    OrderID string
    SKU     string
    Qty     int
}

type ReserveResponse struct {
    Reserved bool
}

func (s *Inventory) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
    if req.Qty <= 0 {
        return ReserveResponse{}, errors.New("qty must be positive")
    }
    return ReserveResponse{Reserved: true}, nil
}
```

No Grove interface is required for this type.

## IDs

```go
const (
    ServiceOrders    grove.ServiceID = 1
    ServiceInventory grove.ServiceID = 2
)

const (
    MethodCreateOrder grove.MethodID = 1
    MethodReserve     grove.MethodID = 1
)
```

## Explicit registration

```go
func RegisterInventory(reg *grove.Registry, inventory *Inventory) error {
    return reg.Register(
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
}
```

This glue is intentionally explicit. It should be small enough that generated code is unnecessary and obvious enough that debugging remains straightforward.

## Orders calling Inventory

```go
type Orders struct {
    grove *grove.Client
}

func (s *Orders) Create(ctx context.Context, req CreateOrderRequest) (Order, error) {
    reserve, err := grove.Call[ReserveRequest, ReserveResponse](
        ctx,
        s.grove,
        ServiceInventory,
        MethodReserve,
        ReserveRequest{
            OrderID: req.OrderID,
            SKU:     req.SKU,
            Qty:     req.Qty,
        },
    )
    if err != nil {
        return Order{}, fmt.Errorf("reserve inventory: %w", err)
    }

    if !reserve.Reserved {
        return Order{}, errors.New("inventory not reserved")
    }

    // Continue normal business workflow.
    return Order{ID: req.OrderID}, nil
}
```

The developer can immediately see that Inventory is a distributed Grove dependency because the call is explicit.

## Local vs remote
The source above does not change when Inventory moves from the same Grovlet to another Grovlet. Grove routing chooses the execution path.

```text
same node:  grove.Call -> local registry -> Inventory.Reserve
remote:     grove.Call -> envelope -> System NATS -> remote registry -> Inventory.Reserve
```

## Unit tests
Business logic stays testable without Grove:

```go
func TestReserve(t *testing.T) {
    svc := &Inventory{}
    got, err := svc.Reserve(context.Background(), ReserveRequest{
        OrderID: "o-1",
        SKU:     "sku-1",
        Qty:     1,
    })
    require.NoError(t, err)
    require.True(t, got.Reserved)
}
```

Grove integration tests separately prove registration, invocation, transport, routing, and failure behavior.