# Durable Execution and Persistent State

**Write normal Go. Add durability where state matters. Grove owns recovery, replay, and durable step results.**

Grove durable execution is explicit. A developer chooses where execution must survive process failure, node failure, restart, or relocation. Grove persists enough execution state to resume the logical operation elsewhere.

## The programming model

A durable execution is composed from named durable steps:

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

A successful step is not merely marked complete. Grove durably stores its successful result together with the step completion record.

On recovery:

```text
completed step
      │
      └── decode persisted result and return it

incomplete step
      │
      └── execute / retry the closure
```

The step closure is not invoked again once Grove has durably recorded that step as completed.

## Full example

```go
package orders

import "grove.dev/grove"

type Order struct {
    ID       string
    Amount   int64
    Customer string
    Status   string
    Tracking string
}

type Payment struct {
    ID     string
    Amount int64
}

type Shipment struct {
    ID       string
    Tracking string
}

type Service struct {
    orders grove.Store[string, Order]

    payments PaymentsClient
    shipping ShippingClient
}

func (s *Service) Fulfill(ctx grove.Context, orderID string) error {
    // Persistent application state. The order survives service/node restarts.
    order, err := s.orders.Get(ctx, orderID)
    if err != nil {
        return err
    }

    // Durable step #1.
    // Payment must be gob-serializable because Grove persists the result.
    payment, err := grove.Step(
        ctx,
        "charge",
        func(ctx grove.Context) (Payment, error) {
            return s.payments.Charge(
                ctx,
                order.Customer,
                order.Amount,
                // Stable for every retry of this logical step.
                grove.IdempotencyKey(ctx),
            )
        },
    )
    if err != nil {
        return err
    }

    // Durable step #2.
    // `payment` is available after recovery from the persisted step result.
    shipment, err := grove.Step(
        ctx,
        "ship",
        func(ctx grove.Context) (Shipment, error) {
            return s.shipping.Create(
                ctx,
                payment.ID,
                grove.IdempotencyKey(ctx),
            )
        },
    )
    if err != nil {
        return err
    }

    // Durable step #3 updates persistent application state.
    _, err = grove.Step(
        ctx,
        "complete-order",
        func(ctx grove.Context) (struct{}, error) {
            order.Status = "completed"
            order.Tracking = shipment.Tracking

            return struct{}{}, s.orders.Put(ctx, order.ID, order)
        },
    )

    return err
}
```

## What happens during failure

Assume Grove has reached this state:

```text
Execution: fulfill/order-42

✓ charge
    result = Payment{ID:"pay-91", Amount:12500}

→ ship
```

Then the node fails.

Grove has durably persisted state conceptually equivalent to:

```text
Execution
  ID: fulfill/order-42

Steps
  charge
    status:          completed
    idempotency-key: fulfill/order-42/charge
    result:          <gob Payment>

  ship
    status:          running
    idempotency-key: fulfill/order-42/ship
```

After recovery, the application code is replayed. When it reaches `Step("charge")`, Grove finds the completed step, decodes the stored `Payment`, and returns it without invoking the closure.

When execution reaches `Step("ship")`, Grove sees that the step was not durably recorded as complete and retries it.

## Retry semantics

The durable-step guarantee is:

> **A durable step that is not known to have completed is retried. A completed step returns its persisted result without running again.**

This means step execution is **at least once**, not exactly once.

There is an unavoidable ambiguity window around external side effects:

```text
1. external provider accepts the request
2. provider performs the side effect
3. node fails
4. Grove never records the step as completed
5. Grove retries the step
```

Grove cannot make an arbitrary external system exactly-once. Instead, every durable step receives a stable idempotency key that is reused across retries:

```text
execution: fulfill/order-42
step:      charge

idempotency key:
grove/fulfill/order-42/charge
```

When the external system supports idempotency, retries become effectively exactly-once at the outcome level.

## Durable step result contract

Successful durable step results are part of Grove's correctness state, not a disposable cache.

Grove should durably retain at least:

```text
execution ID
step ID
status
stable idempotency key
gob-serialized result
retry / error metadata
```

Therefore:

> **Durable boundaries are serialization boundaries.**

Every value returned from `grove.Step` must be Gob-serializable.

Good durable results are portable data:

```go
type Payment struct {
    ID     string
    Amount int64
}
```

Do not return process-local resources such as:

- sockets or open connections
- channels
- functions
- mutexes
- file descriptors
- runtime handles
- pointers whose meaning depends on local process state

## Persistent application state vs durable execution state

These are related but distinct capabilities.

`grove.Store` persists application state:

```go
order, err := s.orders.Get(ctx, orderID)
err = s.orders.Put(ctx, orderID, order)
```

`grove.Step` persists execution progress and step results:

```go
payment, err := grove.Step(ctx, "charge", ...)
```

Together they allow Grove to recover both **what the application knows** and **where a logical operation got to**.

## Developer contract

A developer using durable execution should remember three rules:

1. **Step results must be Gob-serializable.**
2. **An incomplete step may execute more than once.**
3. **External side effects should use `grove.IdempotencyKey(ctx)`.**

Grove handles durable bookkeeping, retry identity, result persistence, replay, recovery, and execution migration. Distribution remains explicit; repetitive failure-recovery machinery moves into the runtime.

## Operational visibility

SDK semantics should automatically become CLI semantics. A service using persistent state and durable execution should expose those capabilities without additional instrumentation.

For example:

```text
$ grove inspect orders

Service: orders

Capabilities
  ✓ Persistent state
  ✓ Durable execution

State
  orders          12,481 entries

Executions
  running              3
  completed       48,291
  recovering            1
  failed                0
```

And execution inspection should expose step-level recovery state:

```text
$ grove executions orders

ID                  STATE        STEP       NODE
fulfill:8912        running      ship       node-2
fulfill:8913        recovering   charge     node-3
fulfill:8914        completed    done       node-1
```

The DevEx principle is simple: **what the developer expresses through the SDK becomes observable and operable through Grove automatically.**
