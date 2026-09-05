# Grove SDK Serialization

## MVP decision
Use Go `encoding/gob` for Grove application invocation payloads and expose it through small SDK helpers.

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

## Separation of concerns
The envelope is transport-independent. System NATS carries encoded envelopes, but NATS-specific concepts must not become part of the application-facing service API.

Likewise, Gob is an MVP implementation choice, not an invitation to expose raw `gob.Encoder` or `gob.Decoder` throughout business services.

## Compatibility model
For the MVP Grove assumes compatible application binaries across participating nodes. Therefore:
- do not add protobuf-style schema evolution machinery
- do not add generated schemas
- do not build version negotiation into the envelope
- fail clearly on incompatible decode rather than hiding it

## Tests
Cover:
- request/response round trip
- malformed payload
- unknown service/method representation
- handler error representation
- request ID preservation
- decode failure with useful error context
