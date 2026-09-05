# Grove Shop Demo UI

## Goal
The browser must tell the complete Grove MVP story on one screen.

The page is split into two persistent areas:

```text
+-----------------------------+---------------------------+
| Orders UI                   | Grove Cluster Status      |
|                             |                           |
| create / inspect orders     | nodes                     |
| order state                 | components / placement    |
| business result             | active + candidate        |
|                             | rollout / rollback state  |
+-----------------------------+---------------------------+
```

Prefer roughly 60% width for Orders and 40% for Grove status on desktop. On narrow screens the sections may stack, but both remain part of the same page.

## Orders pane
The left pane is the real application UI.

Minimum capabilities:
- create an order,
- list recent orders,
- show an order's business progression,
- show final success/failure clearly.

Suggested deterministic progression:

```text
Created -> Reserved -> Paid -> Shipping -> Completed
```

Keep business behavior intentionally small. The demo is proving Grove, not building a storefront.

## Cluster Status pane
The right pane is always visible and continuously polls Grove while the page is open.

It must show at least:
- cluster health,
- node IDs and health,
- component placement and health,
- active artifact/version,
- active embedded-config identity/revision,
- candidate artifact/version when present,
- deployment phase,
- rollback state and reason when present.

The status UI must render intermediate transitions so an operator can watch an upgrade happen live.

Example sequence:

```text
ACTIVE: artifact-A
        |
        v
CANDIDATE STARTING: artifact-B
        |
        v
VALIDATING
        |
        v
Inventory CRASHED
        |
        v
CANDIDATE UNHEALTHY
        |
        v
ROLLBACK
        |
        v
ACTIVE: artifact-A
CLUSTER HEALTHY
```

## Polling contract
For MVP v1, use simple HTTP polling rather than WebSockets or SSE.

Target interval: approximately 500 ms to 1 second.

The browser should consume a structured Grove status/read-model endpoint. It must not scrape logs or infer control-plane state from presentation strings.

Conceptually:

```text
Browser
  |-- business requests --> /api/...
  `-- periodic polling ---> /grove/status
```

The exact route names may evolve, but the behavior is required.

## Important ownership rule
The UI only observes Grove state.

Health detection, candidate rejection, rollback, placement, and reconciliation are Grove responsibilities. The browser must never be required for correctness.

## Serving model
The Web component is itself part of Grove Shop and serves the UI from the same Grove cluster.

UI assets must be embedded in the deployment artifact. No standalone frontend process, separate web server, or loose static asset directory is required for the demo.

## Upgrade behavior
The currently active artifact should remain able to serve the existing browser session while a candidate is being validated. The UI should therefore be able to watch a failed candidate and rollback without disappearing simply because a candidate Web component was unhealthy.

## Testing
UI behavior should be covered at the API/read-model level in normal Go tests. The final E2E must prove that status polling can observe the important rollout states without fixed sleeps.