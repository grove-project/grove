# Grove SDK Design Principles

## Goal
Make distributed Go applications explicit without forcing developers into generated code, framework-owned interfaces, or reflection-heavy magic.

## Required properties
1. **Normal Go first** — business logic is ordinary Go and can be instantiated and unit-tested directly.
2. **Explicit distribution** — the developer can see where a Grove RPC boundary exists.
3. **No required interfaces** — Grove does not require service implementations to satisfy generated or framework-defined service interfaces.
4. **No code generation** — no protobuf-style stubs, build-time generated clients, or hidden source rewriting for the MVP.
5. **No reflection requirement** — registration and dispatch should work from explicit handlers and IDs.
6. **Stable IDs** — service and method identifiers are explicit constants controlled by application code.
7. **One Grove call path** — local and remote Grove invocations use the same application-facing invocation primitive; routing decides execution location underneath.
8. **Concrete implementations stay navigable** — a developer reading a service call or registration should be able to navigate to the real service implementation without following generated glue.
9. **Small SDK surface** — prefer a few explicit primitives over an abstraction hierarchy.
10. **No hidden compatibility machinery in MVP** — Grove's initial same-binary/same-version deployment model means the SDK does not need protobuf-style schema/version negotiation.

## Distribution is not transparent
Grove should remove distributed-systems boilerplate, not erase the fact that a call may cross a process or machine boundary. Calls can fail for transport, destination, timeout, serialization, or remote-handler reasons and should expose meaningful errors.

## Business logic vs Grove glue
A service should look like:

```go
type Inventory struct {
    // normal dependencies
}

func (s *Inventory) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
    // normal business logic
}
```

Grove-specific code belongs at registration and invocation boundaries, not throughout the business implementation.

## Out of scope for MVP
- generated typed clients/stubs
- interface generation
- source annotations/macros
- reflection-discovered methods
- transparent object proxies
- cross-version wire-schema negotiation
- automatic retries that hide call failure semantics
- actor framework semantics
- durable execution semantics
