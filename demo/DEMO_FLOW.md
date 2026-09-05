# Grove Shop MVP Demo Flow

## Goal
Prove Grove's core lifecycle in one short, repeatable scenario that requires minimal operator commands and can be automated as the final MVP E2E.

## Preconditions
- Grove CLI available.
- Grove Shop source available.
- Good customer config at `configs/acme.yaml`.
- Broken customer config at `configs/acme-broken.yaml`.
- Local multi-Grovlet cluster behavior provided by the normal Grove developer workflow.

## Demo sequence

### 1. Deploy the known-good artifact

```bash
grove deploy --config configs/acme.yaml
```

Expected:
- Grove builds/packages the application as needed.
- The config is embedded into the artifact.
- A local multi-Grovlet cluster is started or reused.
- Grove Shop components become healthy.
- Grove prints the Web UI URL.

### 2. Open Grove Shop
The browser shows one page with two areas:
- Orders UI.
- Grove Cluster Status.

The Cluster Status pane is already polling Grove continuously.

### 3. Exercise the application
Create an order.

Expected business progression:

```text
Created -> Reserved -> Paid -> Shipping -> Completed
```

The Cluster Status pane simultaneously shows healthy nodes, component placement, active artifact identity, and active config identity.

### 4. Deploy the broken customer configuration
Keep the browser open and run:

```bash
grove deploy --config configs/acme-broken.yaml
```

No browser action should be required.

### 5. Watch the candidate rollout
The status pane should visibly move through the important states, conceptually:

```text
Artifact A ACTIVE
       |
       v
Artifact B CANDIDATE
       |
       v
Candidate components STARTING
       |
       v
Inventory fails because reservation_buffer < 0
       |
       v
Candidate UNHEALTHY
       |
       v
ROLLBACK / CANDIDATE REJECTED
       |
       v
Artifact A ACTIVE
       |
       v
CLUSTER HEALTHY
```

The exact internal state names may differ, but these transitions must be observable through a structured Grove status read model.

### 6. Verify recovery
Create another order after rollback.

Expected:
- order completes successfully,
- previous known-good artifact remains active,
- previous embedded config remains active,
- no manual config repair or component restart is required.

## Headline demo contract
The demo should require only these meaningful operator commands:

```bash
grove deploy --config configs/acme.yaml
grove deploy --config configs/acme-broken.yaml
```

`grove status` may exist as an optional terminal inspection command but is not required to understand the demo because the browser continuously visualizes cluster state.

## Final E2E mapping
The final MVP automated test should reproduce the same lifecycle programmatically:
1. produce/deploy Artifact A with good embedded config,
2. wait for cluster/application health,
3. execute an order flow,
4. deploy Artifact B with broken config,
5. observe candidate introduction,
6. observe Inventory failure,
7. observe candidate rejection/rollback,
8. verify Artifact A is authoritative again,
9. execute another successful order flow,
10. clean up all processes and temporary state.

No fixed sleeps, manual browser interaction, Docker, or shell orchestration are allowed for acceptance.