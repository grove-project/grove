# Grove SDK Serialization

## MVP decision
Use Go `encoding/gob` for Grove application invocation payloads **and durable step results**, exposed through small SDK helpers.

```go
func Encode[T any](v T) ([]byte, error)
func Decode[T any](data []byte, out *T) error
```

The exact signatures may be adjusted during implementation, but application code should not construct Gob encoders/decoders at every call site.

## Request envelope
Remote requests must carry at least:

```text
RequestID
ServiceID
MethodID
Payload
```

Optional metadata should be added only when a numbered task requires it.

## Response envelope
Responses must carry either:

```text
RequestID
Payload
```

or a structured Grove error sufficient to distinguish transport/dispatch/handler failure classes.

## Durable step results

A successful `grove.Step` result is part of Grove's durable execution state. It is not a disposable cache.

```go
payment, err := grove.Step(
    ctx,
    "charge",
    func(ctx grove.Context) (Payment, error) {
        return payments.Charge(ctx, order)
    },
)
```

`Payment` must be Gob-serializable. When the step completes, Grove persists its result together with the durable completion record. During replay, Grove decodes the persisted result and returns it without invoking the step closure again.

Conceptually:

```text
execution ID
step ID
status = completed
stable idempotency key
result = <gob payload>
```

Therefore:

> **Durable boundaries are serialization boundaries.**

Durable results should be portable data. Do not use process-local resources such as sockets, channels, functions, mutexes, file descriptors, open connections, runtime handles, or pointers whose meaning depends on local process state.

See [Durable execution](DURABLE_EXECUTION.md) for the full recovery and retry contract.

## Separation of concerns
The envelope is transport-independent. System NATS carries encoded envelopes, but NATS-specific concepts must not become part of the application-facing service API.

Likewise, Gob is an MVP implementation choice, not an invitation to expose raw `gob.Encoder` or `gob.Decoder` throughout business services.

## Compatibility model
For the MVP Grove assumes compatible application binaries across participating nodes. Therefore:
- do not add protobuf-style schema evolution machinery
- do not add generated schemas
- do not build version negotiation into the envelope
- fail clearly on incompatible decode rather than hiding it

The same compatibility assumption applies to persisted durable step results: a recovering Grove binary must be able to decode the Gob result produced by the execution it is resuming.

## Tests
Cover:
- request/response round trip
- durable step result round trip
- durable replay returns the stored result without invoking the closure
- unsupported/non-serializable durable results fail with useful context
- malformed payload
- unknown service/method representation
- handler error representation
- request ID preservation
- decode failure with useful error context
