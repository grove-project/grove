Grove SDK: Explicit Service-to-Service Communication

Status: Developer Experience design

Design principles

• No interfaces for service-to-service dependencies. Services depend on concrete Go service types.  
• No auto-generated code and no build-time source rewriting. The code the developer reads is the code that executes and is debugged.  
• Grove does not hide distribution primitives. A remotely callable method explicitly contains its Grove forwarding boundary.  
• Same binary across the cluster. RPC method mappings only need to be deterministic within the binary; cross-version wire compatibility is not a design requirement.  
• Local calls remain ordinary Go calls with no serialization or transport overhead.

Developer model

A service embeds grove.Service, registers the methods that may cross a placement boundary, and places an explicit remote jumper at the beginning of each registered method.

Example: Inventory service

type Service struct {  
    grove.Service  
    db *DB  
}

func New(db *DB) *Service {  
    s := &Service{db: db}

    s.RPC(  
        s.Reserve, // method 0  
        s.Stock,   // method 1  
    )

    return s  
}

func (s *Service) Reserve(  
    ctx context.Context,  
    req ReserveRequest,  
) (ReserveResponse, error) {  
    if s.Remote() {  
        return grove.Call[ReserveResponse](ctx, s, 0, req)  
    }

    return s.db.Reserve(ctx, req)  
}

The RPC registration order deterministically defines the method table:

Inventory  
0 -> Reserve  
1 -> Stock

Service A depends directly on Service B

Orders uses the concrete Inventory type. There is no generated client, Ref type, or interface.

type Service struct {  
    inventory *inventory.Service  
}

func New(inventory *inventory.Service) *Service {  
    return &Service{inventory: inventory}  
}

The business call remains ordinary Go:

reservation, err := s.inventory.Reserve(ctx, inventory.ReserveRequest{  
    SKU: req.SKU,  
    Qty: 1,  
})

IDE navigation therefore goes directly from the call site to inventory.Service.Reserve.

Local communication flow

When Inventory is owned by the current worker:

Orders.Create  
  -> Inventory.Reserve  
  -> s.Remote() == false  
  -> execute Inventory business logic directly

There is no RPC, serialization, socket, NATS, or generated proxy on this path.

Remote communication flow

Each logical service instance has a Grove identity and current owner. For example:

Inventory instance: 17  
Owner: Node B

If Orders is on Node A, its concrete Inventory service object represents logical instance 17 but is marked remote relative to the current worker.

Orders still executes:

s.inventory.Reserve(ctx, req)

The call enters the real developer-written Reserve method. The explicit jumper observes s.Remote() == true and executes:

return grove.Call[ReserveResponse](ctx, s, 0, req)

The call identifies:

• service instance 17  
• method 0 (Reserve)  
• the concrete ReserveRequest

RPC sender

Grove serializes the request payload, initially using a Go-native codec such as gob, and sends an envelope conceptually containing:

ServiceInstanceID: 17  
MethodID: 0  
Payload: <serialized ReserveRequest>

Operational metadata such as request ID, deadline, cancellation and tracing context can accompany the envelope.

Routing

The Grove runtime resolves the current placement:

service 17 -> Node B

and transports the RPC to Node B. The underlying transport is an implementation detail of the runtime and can evolve independently from the SDK contract.

RPC receiver

Node B maintains a registry of locally owned logical service instances:

17 -> *inventory.Service  
25 -> *payment.Service  
31 -> *catalog.Service

The receiver first resolves ServiceInstanceID 17 to the local Inventory object, then resolves MethodID 0 using the deterministic method table created by s.RPC(...).

Typed dispatch

Registration can use Go generics to create typed handlers without reflection or generated code. Registering s.Reserve captures the concrete request and response types and creates the equivalent of a handler that:

1. Deserializes payload into ReserveRequest.  
2. Calls s.Reserve(ctx, req).  
3. Serializes ReserveResponse.  
4. Returns the response bytes.

No reflection is required on the RPC hot path.

Execution on destination

The receiver invokes the same developer-written method:

s.Reserve(ctx, req)

On Node B, instance 17 is locally owned, so s.Remote() == false. The method falls through to its ordinary business logic instead of forwarding again. This prevents an RPC loop.

End-to-end remote path

NODE A

Orders.Create()  
  -> Inventory.Reserve()  
  -> Remote? YES  
  -> grove.Call(instance=17, method=0)  
  -> serialize request  
  -> Grove transport

NODE B

RPC receiver  
  -> resolve instance 17  
  -> resolve method 0  
  -> deserialize ReserveRequest  
  -> Inventory.Reserve()  
  -> Remote? NO  
  -> actual business logic  
  -> serialize ReserveResponse  
  -> response transport

NODE A

  -> decode ReserveResponse  
  -> return from Inventory.Reserve()  
  -> Orders continues normally

Key property

The caller remains normal Go. Distribution is explicit only at the callee's movable service boundary:

CALLER

s.inventory.Reserve(ctx, req)

CALLEE

if s.Remote() {  
    return grove.Call[ReserveResponse](ctx, s, 0, req)  
}

// ordinary Go business logic

This preserves concrete-type navigation, normal debugging and direct local calls while making every distributed boundary visible in source code.

Design statement

Grove does not pretend that distribution is free or invisible. Services are ordinary concrete Go objects. Methods that may cross placement boundaries explicitly declare that capability with a small Grove forwarding gate. Local execution stays a direct Go call; remote execution uses the same method and business logic through Grove's service registry and RPC transport.  
