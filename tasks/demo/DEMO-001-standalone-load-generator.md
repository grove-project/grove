---
id: DEMO-001
status: todo
outcome: groveshop-demo
depends-on:
  - NET-001
---

# Standalone GroveShop load generator with TUI

## Goal
Keep realistic external GroveShop traffic flowing while cluster actions are demonstrated, and make the user-visible impact continuously obvious in a dedicated TUI.

## Standalone binary contract

The load generator is a separate demo binary so it can stay running in its own terminal while GroveShop nodes are manipulated:

```text
./bin/groveshop-load --target http://127.0.0.1:8080
```

Startup enters the load TUI directly. Non-interactive automation may use explicit flags/actions, but the demo must not require Grove-internal addresses or APIs.

The generator behaves like an external client and drives GroveShop only through its public ingress. Cluster failure/recovery and rollout effects are therefore real user-visible effects, not synthetic control-plane metrics.

The load TUI is a first-class demo surface. Its primary question is:

> What happened to real application traffic while the Grove cluster changed?

## TUI contract

The TUI opens immediately when `groveshop-load` starts and remains useful beside the Grove cluster terminals. Business transactions, not internal Grove RPCs, are the primary unit. For the canonical demo, report completed GroveShop orders per second and end-to-end order success.

### Screen: steady state

```text
┌─ GroveShop Load ───────────────────────────────────────────────────────┐
│ ● RUNNING     Target localhost:8080     Scenario checkout     02:14   │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│   ORDERS/s          SUCCESS          P95 LATENCY        IN FLIGHT      │
│     25.1             100.00%             31 ms              12         │
│                                                                       │
│   TOTAL             FAILED           P50 / P99                         │
│    3,412               0             14 / 52 ms                        │
│                                                                       │
├─ Orders/s ─────────────────────────────────────────────────────────────┤
│ 30 ┤                                                                   │
│ 25 ┤███████████████████████████████████████████████████████████████   │
│ 20 ┤                                                                   │
│    └─────────────────────────────────────────────────────────── now    │
├─ Latency p95 ──────────────────────────────────────────────────────────┤
│100 ┤                                                                   │
│ 50 ┤────────────────────────────────────────────────────────────────   │
│  0 ┤                                                                   │
│    └─────────────────────────────────────────────────────────── now    │
│                                                                       │
│ [Space] Pause   [+/-] Load   [M] Marker   [R] Reset   [Q] Quit        │
└───────────────────────────────────────────────────────────────────────┘
```

The headline demo invariant is successful end-to-end orders. The total successful-order counter must keep climbing while cluster topology changes underneath the application.

### Screen: node disruption and recovery

When a node is killed, the external effect must be visible in the same scrolling history. A brief throughput or latency disturbance is acceptable; the screen must make it obvious whether orders actually failed.

```text
┌─ GroveShop Load ───────────────────────────────────────────────────────┐
│ ● RUNNING     Target localhost:8080     Scenario checkout     04:37   │
├───────────────────────────────────────────────────────────────────────┤
│   ORDERS/s          SUCCESS          P95 LATENCY        IN FLIGHT      │
│     24.8             100.00%             44 ms              16         │
│   TOTAL             FAILED           P50 / P99                         │
│    6,948               0             17 / 103 ms                       │
├─ Orders/s ─────────────────────────────────────────────────────────────┤
│ 30 ┤                                                                   │
│ 25 ┤███████████████████████▆▅▆████████████████████████████████████    │
│    │                         ▲                                         │
│    │                    node-2 lost                                    │
│    └─────────────────────────────────────────────────────────── now    │
├─ Latency p95 ──────────────────────────────────────────────────────────┤
│150 ┤                         ╭╮                                        │
│100 ┤                         │╰╮                                       │
│ 50 ┤─────────────────────────╯ ╰───────────────────────────────────    │
│    │                         ▲                                         │
│    │                    node-2 lost                                    │
├─ Events ───────────────────────────────────────────────────────────────┤
│ 09:54:21  NODE LOST      node-2                                       │
│ 09:54:22  SERVICE MOVE   checkout → node-1                            │
│ 09:54:22  RECOVERED      p95 back to baseline                         │
└───────────────────────────────────────────────────────────────────────┘
```

Cluster labels such as `NODE LOST` and `SERVICE MOVE` may appear only when supplied by an explicitly separate Grove observation/event source. The workload path itself remains an ordinary external client through public ingress. If no observation source is connected, the same graphs render without Grove event labels and the operator may add a manual marker.

### Screen: binary/config rollout

The load TUI must remain running through a rollout and correlate rollout events with the external traffic history.

```text
┌─ GroveShop Load ───────────────────────────────────────────────────────┐
│ ● RUNNING     25 orders/s                 SUCCESS 100.00%              │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│      11,821 / 11,821 ORDERS SUCCEEDED                                 │
│                                                                       │
├─ Orders/s ─────────────────────────────────────────────────────────────┤
│ 30 ┤                                                                   │
│ 25 ┤██████████████████████████████████████████████████████████████    │
│    │                       ▲                         ▲                   │
│    │                  rollout start            rollout complete        │
│    └─────────────────────────────────────────────────────────── now    │
├─ Latency p95 ──────────────────────────────────────────────────────────┤
│100 ┤                                                                   │
│ 50 ┤────────────────────────╮ ╭────────────────────────────────────    │
│ 25 ┤                        ╰─╯                                        │
│    └─────────────────────────────────────────────────────────── now    │
├─ Grove Events ─────────────────────────────────────────────────────────┤
│ 10:02:11  ROLLOUT       v1 → v2                                      │
│ 10:02:12  v2            node-3 ready                                  │
│ 10:02:14  v2            node-1 ready                                  │
│ 10:02:16  v2            node-2 ready                                  │
│ 10:02:16  ROLLOUT       completed                                     │
└───────────────────────────────────────────────────────────────────────┘
```

Embedded-configuration rollouts use the same visualization because Grove treats the resulting binary as another immutable deployment artifact.

### Screen: user-visible degradation

Failures must be visually prominent rather than hidden in aggregate statistics.

```text
┌─ GroveShop Load ───────────────────────────────────────────────────────┐
│ ⚠ DEGRADED     25 orders/s                  SUCCESS 98.73%             │
├───────────────────────────────────────────────────────────────────────┤
│   SUCCESSFUL         FAILED          P95 LATENCY        IN FLIGHT      │
│     14,201             183              841 ms              47          │
├─ Failures ─────────────────────────────────────────────────────────────┤
│ 10:08:41  ✗ checkout     503       812ms                              │
│ 10:08:41  ✗ checkout     503       921ms                              │
│ 10:08:42  ✗ checkout     timeout   2.0s                               │
└───────────────────────────────────────────────────────────────────────┘
```

Do not continuously print successful requests. Successful traffic is represented by counters and graphs; individual failures deserve event-level visibility.

### Screen: interactive load change

The operator can change target load without restarting the binary. The graph must preserve enough recent history to show the effect.

```text
┌─ GroveShop Load ───────────────────────────────────────────────────────┐
│ ● RUNNING                                                              │
│                                                                       │
│ Target rate        25 → 50 → 100 → 200 orders/s                       │
│                                      ▲                                │
│                                     NOW                               │
│ Actual             199.4 orders/s                                     │
│ Success            100.00%                                            │
│ p95                 73 ms                                             │
│ In flight           91                                                │
├─ Throughput ───────────────────────────────────────────────────────────┤
│200 ┤                                          ████████████████████     │
│150 ┤                              ████████████                         │
│100 ┤                    ██████████                                     │
│ 50 ┤          ██████████                                               │
│ 25 ┤██████████                                                         │
│    └────────────────────────────────────────────────────────── now     │
│                                                                       │
│ [-] Less load                                      More load [+]      │
└───────────────────────────────────────────────────────────────────────┘
```

## Interaction

Required demo controls:

- `Space` — pause/resume workload generation.
- `+` / `-` — increase/decrease target order rate live.
- `M` — add a manual timeline marker.
- `R` — reset displayed statistics/history.
- `Q` — quit.

The implementation may additionally expose non-interactive equivalents for automated tests.

## Data separation

Keep workload execution and Grove observation explicitly separate:

```text
                       groveshop-load
                    ┌──────────────────┐
 public ingress ───►│ Workload engine  │────► external metrics
                    │                  │
 Grove events ─────►│ Event observer   │────► timeline annotations
                    └────────┬─────────┘
                             │
                             ▼
                            TUI
```

The workload engine determines success only from the actual GroveShop public application interaction. Grove events may explain graph changes but must never manufacture or override workload success.

## Scope

Give the standalone load generator its own TUI showing:

- target and actual completed orders/second;
- successful and failed end-to-end orders;
- success percentage;
- in-flight transactions;
- p50, p95, and p99 end-to-end latency;
- scrolling throughput and p95 latency history;
- prominent recent failures;
- optional Grove lifecycle annotations;
- manual timeline markers;
- interactive load adjustment.

Keep the display readable when tiled beside three Grove node terminals.

## Acceptance

During the canonical demo:

1. Start `groveshop-load`; it enters the TUI directly and establishes steady checkout traffic through public ingress.
2. Kill a Grove node. The timeline shows the externally observed effect while traffic continues; if Grove event observation is enabled, the cluster event is aligned with the graph.
3. Service recovery is visible as throughput/latency return to steady state.
4. Build/start a new GroveShop artifact and perform the rollout without restarting the load tester. Rollout progress can be annotated while the successful-order counter continues.
5. The same behavior applies to an embedded-configuration rollout.
6. Change target load interactively and observe actual throughput converge without restarting the tester.
7. Any failed order is clearly visible and included in aggregate failure metrics.
8. Without a Grove event source, all workload metrics still work and the tester remains a valid independent external observer.

Exact graph glyphs may vary, but these information relationships and temporal behavior are required.

## Tests

- Unit-test the load-state/window model independently from terminal rendering.
- Unit-test percentile, throughput, success/failure, in-flight, and history calculations.
- Test pause/resume, live rate changes, reset, and manual markers.
- Test optional event annotations independently from workload success accounting.
- Integration-test the TUI model against the real GroveShop public ingress across failure/recovery and rollout transitions.
- Manually verify the canonical four-terminal demo layout.
