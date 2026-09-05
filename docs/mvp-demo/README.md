# Grove MVP Demo — Grove Shop

## Purpose
Grove Shop is the permanent reference/demo application for the MVP. It must demonstrate Grove through a small real application rather than a synthetic infrastructure test.

The demo proves this lifecycle:

`normal Go app -> embedded customer config -> immutable artifact -> multi-node Grove deployment -> live observation -> candidate upgrade -> config-induced failure -> automatic rollback -> healthy application`

## Demo application
Grove Shop is a small order-processing application with these logical components:

- `web` — serves the embedded Web UI and HTTP business API.
- `orders` — owns the order workflow.
- `inventory` — reserves inventory.
- `payment` — performs deterministic fake payment processing.
- `shipping` — performs deterministic fake shipment creation.

Keep business behavior deliberately small and deterministic. The application exists to exercise Grove, not to become a commerce product.

## Single artifact rule
The deployable Grove Shop artifact contains:

- application code;
- all service components;
- Web UI HTML/CSS/JavaScript assets;
- Grove deployment metadata;
- a reserved customer-configuration region;
- the customer configuration embedded after compilation.

There must be no separately deployed dashboard, frontend server, or loose production configuration file required to run the demo.

## Minimal user workflow
The intended public demo workflow is:

```bash
grove deploy --config configs/acme.yaml
```

The command builds/packages as necessary, embeds the selected customer configuration, starts or uses the local Grove cluster, deploys the artifact, waits for the application to become usable, and prints the Web UI URL.

The user leaves that browser page open and then runs:

```bash
grove deploy --config configs/acme-broken.yaml
```

The browser must show the candidate rollout, component failure, health detection, rollback, and return to the previous healthy artifact without requiring a page reload.

A diagnostic command may also exist:

```bash
grove status
```

Do not require the user to manually create a cluster, add nodes, compile config, embed config, create an artifact, or orchestrate upgrade steps for the demo.

## Required demo sequence
1. Build/package Grove Shop.
2. Embed `acme.yaml` into the artifact.
3. Start a local multi-Grovlet cluster using real Grovlet OS processes.
4. Deploy Artifact A.
5. Open the Web UI served by the Grove cluster.
6. Create an order and complete the deterministic order flow.
7. Keep the browser open.
8. Deploy Artifact B using `acme-broken.yaml`.
9. Start B as a candidate while A remains the known-good deployment.
10. The bad embedded configuration causes a selected component to fail deterministically.
11. Grove observes that the candidate is unhealthy.
12. Grove rejects/rolls back B and makes/keeps A authoritative.
13. The Web UI continuously reflects every cluster/deployment transition.
14. The order application is healthy again on Artifact A.

## Configuration failure
The broken configuration must represent a realistic invalid application setting rather than requiring Grove-specific failure logic. Example:

```yaml
inventory:
  reservation_buffer: -1
```

The Inventory component may reject the value during startup or fail deterministically because of it. The important property is that the failure originates from application behavior driven by the embedded configuration and Grove detects the resulting unhealthy candidate.

Do not make rollback depend on the Web UI. The UI is only an observer.

## What the demo proves
- normal Go application code;
- explicit Grove service communication;
- multiple real Grovlet processes;
- System NATS control communication;
- JetStream/KV authoritative control state;
- one immutable application artifact;
- post-build embedded customer configuration;
- Web UI embedded in and served from the Grove application;
- live cluster/deployment visibility;
- candidate deployment and health validation;
- automatic rejection/rollback of a bad candidate;
- restoration/preservation of the previous known-good artifact and configuration.

## Not MVP v1
Do not add debugging/DAP/Delve to this demo for MVP v1. Debugging is a natural MVP v2 extension: select a Grove component, inspect it, and attach through Grove without manually locating its process/node/debugger endpoint.