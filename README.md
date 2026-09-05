# Grove 🍁

**Build, run, debug, and ship a distributed Go application as if it were one program.**

One binary. One cluster. Built-in RPC, ingress, configuration, resilience, upgrades, and debugging.

```go
grove.Register(OrderServiceID, CreateOrderID, createOrder)

// Distribution is explicit. No generated code. No magic.
result, err := grove.Call[CreateOrderResponse](
    ctx, OrderServiceID, CreateOrderID, request,
)
```

```bash
go build -o shop .
./shop
```

**That's a Grove cluster.**

Start with one binary. Add nodes when you need them. Move services without rewriting them. Debug the cluster from your IDE. Test failures against your real E2E tests. Ship configuration with the application.

**Your application stays a Go application. Distribution becomes a runtime capability.**

```text
Development                  Production

./shop                       ./shop
  │                            │
  └─ Grove                    ├─ Grove
     ├─ API                   ├─ Grove
     ├─ Orders                └─ Grove
     └─ Inventory

        same binary → same app → more nodes
```

Grove is opinionated about one thing: **distributed systems shouldn't force developers to stop thinking in terms of their application.**

[Quickstart](docs/getting-started/) · [Why Grove?](docs/vision/) · [Architecture](docs/architecture/) · [Developer Experience](docs/developer-experience/)
