---
id: DEMO-002
status: todo
outcome: groveshop-demo
depends-on:
  - DEMO-001
---

# Load generator TUI

## Goal
Make the impact of Grove cluster actions visually obvious during the demo.

## TUI contract

Normal operation:

```text
 GROVESHOP LOAD                                      RUNNING ●

 Target       http://127.0.0.1:8080
 Rate         50 req/s

 Throughput   49.8 req/s       Total       18,421
 Success      99.7%            Errors      0.3%
 p50          18 ms
 p95          43 ms
 p99          71 ms

 Recent
 ─────────────────────────────────────────────────────────────
 req/s    █████████████████████████████████████████████  50
 errors   ▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏

 [↑/↓] rate   [p] pause/resume   [r] reset stats   [q] quit
```

The same screen during a cluster disruption should make the transient impact obvious without pretending the load generator understands Grove internals:

```text
 GROVESHOP LOAD                                      RUNNING ●

 Rate         50 req/s
 Throughput   47.1 req/s
 Success      94.2%        Errors  5.8%
 p95          181 ms

 Recent
 ─────────────────────────────────────────────────────────────
 req/s    ████████████████████████▆▃▅██████████████████
 errors   ▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏███▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏
                            ↑ transient impact
```

The TUI reports what the external client observes. It must not label an event as "node failure" or "rollout" unless that information comes from an explicitly separate optional observation source; the core load generator remains independent of Grove internals.

## Scope
Give the standalone load generator its own TUI showing current request rate, success/failure rate, latency, totals, and recent state/history. Keep the display readable beside the Grove node terminals.

## Acceptance
During the canonical demo, the load TUI visibly reacts while a node is killed, services recover, and a new build rolls out, while continuing to show ongoing traffic. Exact graph glyphs may vary, but the metrics and temporal behavior above are required.

## Tests
Test the load-state model independently from terminal rendering and manually verify the tiled-terminal demo layout.
