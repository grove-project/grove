# Grove Shop Demo Architecture

## Application shape

Grove Shop is intentionally small but genuinely distributed:

```text
Browser
   |
   v
Web
 |
 v
Orders
 |-- Inventory
 |-- Payment
 `-- Shipping
```

The exact business logic should remain deterministic and boring. The value is in the Grove lifecycle around it.

## Components

### Web
- Serves embedded HTML/CSS/JavaScript assets.
- Exposes the Orders HTTP API used by the browser.
- Exposes/read-proxies the Grove status endpoint used by the Cluster Status pane.
- Runs as a Grove-managed component inside the same cluster as the rest of the application.

### Orders
- Creates orders and coordinates the order flow.
- Calls Inventory, Payment, and Shipping through Grove's explicit invocation model as those capabilities become available.

### Inventory
- Reserves inventory for an order.
- Owns the configuration value used by the intentional bad-config scenario.

### Payment
- Produces a deterministic successful payment result for the MVP.

### Shipping
- Produces a deterministic shipping result for the MVP.

## Artifact contract

A deployed Grove Shop artifact contains:

```text
Grove Shop artifact
├── application code
│   ├── Web
│   ├── Orders
│   ├── Inventory
│   ├── Payment
│   └── Shipping
├── embedded Web UI assets
├── Grove deployment/runtime metadata
└── reserved embedded customer-config region
```

There must be no separate frontend deployment and no required loose UI assets or production config files accompanying the artifact.

## Runtime topology

The MVP E2E topology is multiple real Grovlet OS processes on one host:

```text
Host
├── Grovlet A
├── Grovlet B
└── Grovlet C
```

Example placement during a demo:

```text
Grovlet A        Grovlet B        Grovlet C
---------        ---------        ---------
Web              Inventory        Payment
Orders           Shipping
```

Placement may change as later tasks introduce recovery and upgrade behavior. The demo must never rely on same-process or same-host shortcuts for distributed behavior.

## Grove control plane

The demo follows the accepted Grove architecture:
- Core NATS subjects/request-reply for transient control and RPC traffic.
- JetStream/KV for authoritative replicated control state.
- No custom Grove Raft implementation.
- No process-local map is authoritative cluster truth.

## Upgrade model

An upgrade operates on complete immutable artifacts.

```text
Artifact A = app + UI + config A
Artifact B = app + UI + config B
```

Artifact A remains the known-good active deployment while Artifact B is introduced as a candidate. Candidate health is observed by Grove. If B fails validation/health, Grove rejects B and converges back to A.

The browser is an observer of this lifecycle. It does not perform health detection or rollback itself.