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

## Scope
Give the standalone load generator its own TUI showing current request rate, success/failure rate, latency, totals, and recent state/history. Keep the display readable beside the Grove node terminals.

## Acceptance
During the canonical demo, the load TUI visibly reacts while a node is killed, services recover, and a new build rolls out, while continuing to show ongoing traffic.

## Tests
Test the load-state model independently from terminal rendering and manually verify the tiled-terminal demo layout.
