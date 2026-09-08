# Grove SDK Capabilities

A plain Go service is valid Grove code. Capabilities are optional, explicit patterns that give Grove enough semantic knowledge to provide stronger guarantees, testing, diagnostics, or optimization.

```text
explicit pattern
    ↓
Grove gains semantic knowledge
    ↓
runtime unlocks stronger behavior
    ↓
tests exercise the guarantee
    ↓
CLI explains the value
```

## Examples

A durable execution capability can imply:

```text
Durable execution
   ├─ recoverable
   ├─ resumable
   ├─ execution-migratable
   └─ inspectable / replayable
```

A partitioned execution capability can tell Grove that independent partitions may run concurrently while same-partition work may require serialization.

An idempotent operation can make automatic retries safe.

Properties Grove can prove should be derived automatically rather than exposed as redundant configuration.

## Trade-offs are part of the contract

Capabilities are not universally good. Documentation and CLI output should show both what a capability unlocks and what it costs.

For example, durable execution adds recovery and resumability, but persistence on the execution path may be inappropriate for latency-sensitive real-time work.

## Mobility

Whole-service mobility and durable-execution mobility are different concepts. A service can be constrained by host-local files, raw sockets, devices, or other node-local resources even when an individual durable execution can resume elsewhere.

Useful service mobility classes are:

- **Seamless** — suspension and relocation should not cause observable application failure.
- **Reconnect** — execution state can move, but external resources must reconnect or rebind.
- **Pinned** — node-local or physical resources prevent transparent relocation.

Grove should explain *why* mobility was weakened rather than merely label a service non-migratable.

## Capability design rule

A new SDK capability should exist only when explicit adoption gives Grove meaningful knowledge that changes runtime behavior, testing, diagnostics, optimization, or available operations.