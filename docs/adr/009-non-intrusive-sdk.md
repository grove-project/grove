ADR-009 — Non-Intrusive SDK and Native Go Service Contracts

Status: Proposed  
Date: 2026-08-30

Context

Grove aims to make distributed applications intuitive for developers ranging from juniors to experienced distributed-systems engineers while preserving a natural Go development style. The runtime must provide service routing, distribution, testing, resilience validation, storage, messaging, and observability without forcing business code into a Grove-specific programming model.

A particularly important use case is E2E testing. Developers should define normal business flows using their application's own packages, interfaces, request/response types, constants, and documentation. Grove can then deploy the application and execute those flows across an automatically derived resilience matrix. Native Go types also provide normal gopls autocomplete and compile-time checking.

Decision

Grove will be opinionated about distributed behavior and minimally opinionated about application code structure.

Business packages SHOULD NOT need to import Grove. Grove integration SHOULD primarily occur at composition and execution boundaries such as main, bootstrap code, infrastructure adapters, and test harnesses.

Applications define service contracts using normal Go interfaces and normal application-owned types. Grove does not require developers to duplicate those contracts in a Grove IDL or generated developer-facing model.

Example application contract:

type API interface {  
    Purchase(context.Context, PurchaseRequest) (Order, error)  
    Refund(context.Context, OrderID) error  
}

Business services receive dependencies through ordinary Go dependency injection:

type Checkout struct {  
    payments payments.API  
}

func New(p payments.API) *Checkout {  
    return &Checkout{payments: p}  
}

This allows the same business package to run with a real Grove-provided implementation, a local implementation, or a unit-test fake without changing its business logic.

Grove SDK Role

The Grove SDK should focus on runtime boundaries rather than business logic:

• application/service registration and lifecycle  
• resolving or constructing distributed service clients  
• infrastructure adapters such as storage and messaging  
• E2E cluster connectivity  
• runtime inspection and test control

Grove-specific capabilities remain opt-in. Applications may define their own storage, event, and dependency interfaces and wire Grove implementations at composition time.

Code Generation

Code generation is not a fundamental requirement of the Grove programming model.

The SDK should first attempt to provide a strongly typed and natural developer experience without generated developer-facing packages. If transparent remote implementations of arbitrary existing Go interfaces are required, Grove may generate small proxy implementations because Go cannot dynamically synthesize methods for arbitrary interfaces at runtime.

If generation is used, Grove MUST generate implementations rather than replacement application types. The developer continues to own and import types such as checkout.API, checkout.PurchaseRequest, and checkout.Order. Generated code is disposable transport plumbing and should remain invisible during normal development.

Testing Model

Developers define E2E functionality using their own application APIs and assertions. Grove should not introduce a separate E2E DSL when existing test frameworks are sufficient.

Conceptually:

Developer owns:  
• business flow  
• correctness assertions  
• optional SLOs or qualitative expectations

Grove owns:  
• deployment of the real application  
• observation of the baseline execution path  
• topology and dependency discovery  
• generation of relevant failure scenarios  
• fault injection timing  
• repeated execution of the same E2E flow  
• functional and performance measurements

Resilience is therefore not a separate test suite. Grove reruns the developer's actual E2E flows under relevant resilience conditions and verifies that business functionality continues to work.

SLOs are optional. When developers do not yet know appropriate targets, Grove should measure behavior and help them discover realistic SLOs rather than forcing arbitrary numbers.

Repository Structure

Grove does not prescribe a services/, internal/, cmd/, pkg/, or tests/e2e/ layout. Existing Go repositories should be first-class Grove applications.

Grove defines semantic contracts and conventions, not directory organization.

Consequences

Positive:

• Existing Go code remains reusable outside Grove.  
• Unit testing remains ordinary Go testing.  
• Developers retain native gopls autocomplete, GoDoc, refactoring, and compile-time types.  
• Existing applications can adopt Grove incrementally.  
• Grove can provide sophisticated distributed behavior without dominating application architecture.  
• E2E tests describe business behavior rather than infrastructure failure procedures.

Trade-offs:

• Grove must maintain clear rules for which Go interface signatures can cross process or node boundaries.  
• Some advanced distributed primitives may require explicit Grove APIs.  
• Transparent arbitrary-interface proxies may require internal code generation.  
• Avoiding a Grove-specific IDL means contract compatibility and serialization rules must be derived from Go contracts carefully.

Guiding Principles

Grove should integrate through interfaces, not infect code through APIs.

Grove should own wiring and execution, not the application's code structure or programming model.

The developer specifies what the application must keep doing; Grove determines how to challenge it under distributed-system failures.

Litmus Test

A business package should be usable in a plain Go program or unit test without starting Grove. If removing Grove requires rewriting the application's business logic, the SDK has become too intrusive.  
