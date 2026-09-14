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
```

On a developer terminal, the application binary is also its operational console:

```text
┌ Shop ────────────────────────────────────────────────┐
│ Cluster: healthy     Nodes: 1      Version: local   │
├──────────────────────────────────────────────────────┤
│ > Services                                           │
│   Nodes                                              │
│   Deployments                                        │
│   Configuration                                      │
│   Logs                                               │
│   Debug                                              │
└──────────────────────────────────────────────────────┘
```

Add nodes without changing the application:

```text
Laptop                         Cluster

./shop                         ./shop   ./shop   ./shop
  ├─ API                         ├────────┼────────┤
  ├─ Orders                     same application
  └─ Inventory                  same version
```

The same binary also carries the operational vocabulary for the running application. Humans use the TUI; automation can invoke structured actions underneath it.

```bash
$ ./shop action cluster.status
Cluster     healthy
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Version     v0.8.2
Config      production-42
```

Developers can also register application-specific administrative actions into the same console, so an application does not need a second admin utility.

## Start here

| | |
|---|---|
| **[Concepts](docs/concepts.md)** | Understand Grove's mental model: services, workers, Grovlets, nodes, clusters, ingress, and RPC. |
| **[SDK](sdk/)** | Write ordinary Go services and make distributed boundaries explicit. |
| **[Application Console](docs/cli/)** | Operate the application through its built-in TUI and structured actions. |
| **[Operations](docs/operations/)** | Understand cluster state, changes, failures, and recovery. |
| **[Vision](docs/vision/vision.md)** | Why Grove treats a distributed application as one product. |

> **Project status:** Grove is under active development. Examples in the docs define the intended experience; some runtime behavior and console actions may not be implemented yet.