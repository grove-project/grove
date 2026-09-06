# Grove 🍁

**Build, run, debug, and operate a distributed Go application as one coherent system.**

Grove keeps the application familiar: ordinary Go code, explicit distribution, one versioned artifact, and one runtime model from laptop to cluster.

```go
grove.Register(OrderServiceID, CreateOrderID, createOrder)

// A distributed boundary looks distributed.
order, err := grove.Call[CreateOrderRequest, CreateOrderResponse](
    ctx, client, OrderServiceID, CreateOrderID, request,
)
```

```bash
$ go build -o shop .
$ ./shop

Grove cluster ready
  nodes      1
  services   3
  ingress    http://localhost:8080
```

Add nodes without changing the application:

```text
Laptop                         Cluster

./shop                         ./shop   ./shop   ./shop
  ├─ API                         ├────────┼────────┤
  ├─ Orders                     same application
  └─ Inventory                  same version
```

When something changes, Grove should tell you what happened rather than make you reconstruct it from infrastructure:

```bash
$ grove status

Cluster     healthy
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Version     v0.8.2
Config      production-42

Last event
  orders recovered after config rollback
```

## Start here

| | |
|---|---|
| **[SDK](sdk/)** | Write ordinary Go services and make distributed boundaries explicit. |
| **[CLI](docs/cli/)** | Run, inspect, test, debug, deploy, and recover the application. |
| **[Operations](docs/operations/)** | Understand cluster state, changes, failures, and recovery. |
| **[Vision](docs/vision/vision.md)** | Why Grove treats a distributed application as one product. |

Want the machinery underneath? Read the **[architecture](docs/architecture/system-architecture.md)** and **[ADRs](docs/adr/)**.

> **Project status:** Grove is under active development. Examples in the docs define the intended experience; some commands and runtime behavior may not be implemented yet.