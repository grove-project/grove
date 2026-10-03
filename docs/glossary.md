# Glossary

One definition per term, and the name the term has in code. Use these words
in docs, code comments, logs and the console. [concepts.md](concepts.md)
explains how the pieces fit together. [architecture/packages.md](architecture/packages.md)
says which package owns each piece.

## Application

| Term | Meaning | In code |
|---|---|---|
| **Application** | A Go program built with the Grove SDK. One binary is the operator console, the Grovlet, and its own application runtime and workers, depending on how it is started. | `runtime.Definition`, started with `runtime.Main` |
| **Artifact** | The immutable application binary together with its embedded configuration, identified by its digest. It is the unit of deployment. | `internal/artifact` |
| **Service** | A logical capability of the application, such as `orders` or `payments`. It is addressed by a service ID and is independent of where it runs. | `grove.ServiceID` |
| **Component** | How an application declares a service to the runtime: its service ID, name, `Register` hook, optional HTTP handler and ingress routes, and declared handlers. "Component" is the declaration and "service" is what it provides. The status views and TUI use the two interchangeably. | `runtime.Component` |
| **Handler** | One method of a service: a function from request bytes to response bytes, registered under a service ID and method ID. | `grove.Handler`, `grove.MethodID`, registered with `grove.Registry.Register` |
| **Declared handler** | A handler a component lists in `Handlers`. Grove places declared handlers individually (see handler placement). | `runtime.HandlerSpec` |
| **Automatic handler** | A declared handler that runs on every node hosting its component. Calls are spread across them. | `HandlerSpec` without `Exclusive` |
| **Exclusive handler** | A declared handler with exactly one active owner in the cluster at a time. | `HandlerSpec{Exclusive: true}` |
| **Capability** | The name of what an exclusive handler owns. Its workload claims it with `grove.Exclusive` and must stop when ownership is lost. | `HandlerSpec.Capability`, `grove.Exclusive`, `grove.Ownership` |
| **Ingress component** | A component with an HTTP handler that serves the cluster's external address. It runs on the node that serves that address. | `Component.HTTPHandler`, `Component.Routes` |

## Runtime and processes

| Term | Meaning | In code |
|---|---|---|
| **Node** | One logical participant in a cluster. Its identity is independent of the host, so several nodes can share a machine. | node ID, e.g. `node-1` |
| **Grovlet** | The supervisor process of one node. It runs System NATS and the control plane, and it starts and supervises the node's application processes. It never runs application code. | `runtime` package, started by the application binary with node flags |
| **Application runtime** | The one process per node that runs every hosted service by default, as goroutines sharing the process. | process ID `app-runtime-N`, execution mode `in-process` |
| **Worker** | A dedicated process running one service that was isolated explicitly. It is not a general execution environment and not a goroutine. | process ID `worker-N`, execution mode `isolated-process`, `--component-isolate` |
| **Application process** | Either of the above: a process that runs application code. | `process_id`, `pid` in component status |
| **Execution mode** | Whether a service runs in the application runtime or in its own worker. | `controlplane.ExecutionMode` |
| **Service instance** | One running copy of a service on one node. A restart creates a new generation. | `worker_id`, e.g. `orders-1`. Despite the name, it names the instance generation, not a worker process. |
| **Hosting** | Which components a node runs. By default every node hosts every component. | `runtime` startup |
| **Cluster** | The nodes running one application and sharing one control plane. A single node is a cluster of one. | cluster name in node config |

See [architecture/process-model.md](architecture/process-model.md) for the process model.

## Placement

| Term | Meaning | In code |
|---|---|---|
| **Placement** | Where work runs: which nodes. It is decided separately from execution, which decides which process. | `internal/placement` |
| **Handler placement** | The primary level. For each declared handler, the nodes that serve it, with a fencing epoch for exclusive handlers. It is recomputed every reconcile tick. | `controlplane.HandlerPlacement` in `GROVE_HANDLERS` |
| **Service placement** | Which single node owns a service's endpoint. It is used for the ingress and for handlers a component does not declare. It is set at startup and moved by recovery. | `controlplane.PlacementRecord` in `GROVE_PLACEMENT` |
| **Endpoint** | The address that serves a placed service or handler on one node and in one process. | `nats-subject://…` |
| **Live node** | A node whose health is healthy. A **failed** node was seen healthy and is now unavailable. A node never seen is still joining. | `placement.Member`, `controlplane.Members` |
| **Coordinator** | The lowest live node ID. It acts for the cluster when one node must, for example during recovery. | `placement.Coordinator` |
| **Recovery** | Moving the service placements of a failed node to the coordinator. | `placement.Recover` |
| **Lease** | An exclusive handler owner's renewable claim on its capability. | `placement.Lease`, `placement.HeldLease` |
| **Epoch** | The fencing number of an exclusive handler's placement. It increases whenever the owner changes, and a holder at an older epoch must stop. | `HandlerPlacement.Epoch`, `placement.FencedEpoch` |

See [architecture/placement.md](architecture/placement.md) for the placement model.

## Control plane and operations

| Term | Meaning | In code |
|---|---|---|
| **Control plane** | The cluster's shared records and the rules over them: membership, health, desired state, deployments, placement and leases. | `internal/controlplane`, which is pure, stored by `internal/systemnats` |
| **System NATS** | The embedded NATS server and JetStream key-value store each Grovlet runs. It stores the control plane and carries Grove RPC and control requests. | `internal/systemnats` |
| **Data NATS** | A planned application-facing messaging fabric, separate from System NATS. It is not implemented. | none |
| **Bootstrap witness** | A second JetStream peer the founding node runs until enough nodes join for quorum. It is then released and never restarted. | `<node>-peer`, `systemnats.Server.ReleaseWitness` |
| **Membership** | The nodes that joined the cluster and have not left. | `GROVE_MEMBERSHIP` |
| **Health** | Each member's derived state, healthy or unavailable, computed from heartbeats. | `controlplane.EvaluateHealth`, `ClusterView` |
| **Desired state** | Which artifact the cluster should run. | `GROVE_DESIRED` |
| **Deployment** and **rollout** | Recording a new artifact as desired, moving the cluster to it with health gates, and committing or rolling back. | `internal/rollout`, `GROVE_DEPLOYMENTS` |
| **Grove RPC** | A call from one service to another by service ID and method ID. Grove resolves the destination through placement. | `grove.Call` with a `grove.Client` |
| **Inspection** | The read-only view of what is running, used by the console, the TUI and `grove status`. | `internal/inspect` |
| **Console** | The operator interface the application binary opens when run without node flags. It offers actions and views over a local cluster. | `console` (actions), `runtime` (console host), `internal/tui` |
| **Scenario** | An application's own probes, which `grove test` and `grove deploy` run against a cluster. | `runtime.Scenario`, `internal/scenario` |

## Testing

| Term | Meaning | In code |
|---|---|---|
| **TestCluster** | An in-memory multi-node simulation that runs production's placement, health and lease rules on a controllable clock. | `grovetest.NewTestCluster` |
| **Real-process test** | A test that builds the application and starts real Grovlet processes. It is skipped under `-short`. | `grovetest.StartNode`, `internal/testbin` |

See [architecture/testing.md](architecture/testing.md) for the test layers.

## Retired terms

| Do not write | Write instead |
|---|---|
| Grovelet | Grovlet |
| worker, for goroutines inside a process | application runtime |
| worker, for any execution environment | application process, or name the kind |
