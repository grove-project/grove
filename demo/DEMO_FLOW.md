# Grove Shop MVP Demo Flow

## Goal
Prove Grove's core lifecycle in one short, repeatable scenario using the Grove Shop application binary as the operator surface.

## Preconditions
- Grove Shop source available.
- Good customer config at `configs/acme.yaml`.
- Broken customer config at `configs/acme-broken.yaml`.
- Local multi-Grovlet cluster behavior provided by the normal Grove developer workflow.
- No separately installed `grove` CLI is required.

## Demo sequence

### 1. Start the Grove Shop application console

```bash
./bin/groveshop
```

Expected:
- the same binary contains Grove Shop, the Grove runtime integration, and the Grove operational console;
- an interactive terminal opens the Grove TUI;
- no cluster has to be started separately.

### 2. Deploy the known-good artifact

From the TUI:

```text
Deployments > New rollout > configs/acme.yaml
```

Expected:
- Grove embeds the selected configuration and starts the local three-Grovlet cluster;
- the config is embedded into the candidate artifact;
- Grove Shop components become healthy;
- the console exposes the Web UI URL.

For deterministic automation, the equivalent structured action is invoked from the same binary:

```bash
./bin/groveshop action rollout.start --config configs/acme.yaml
```

### 3. Open Grove Shop
The browser shows one page with two areas:
- Orders UI.
- Grove Cluster Status.

The Cluster Status pane is already polling Grove continuously. The TUI may remain open at the same time and should show the same authoritative cluster state.

### 4. Exercise the application
Create an order.

Expected business progression:

```text
Created -> Reserved -> Paid -> Shipping -> Completed
```

The Cluster Status pane and terminal console simultaneously show healthy nodes, component placement, active artifact identity, and active config identity.

### 5. Deploy the broken customer configuration
Keep the browser open and use the TUI:

```text
Deployments > New rollout > configs/acme-broken.yaml
```

For automation:

```bash
./bin/groveshop action rollout.start --config configs/acme-broken.yaml
```

No browser action should be required.

### 6. Watch the candidate rollout
The status pane and TUI should visibly move through the important states, conceptually:

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

The exact internal state names may differ, but these transitions must be observable through a structured Grove status read model shared by the Web UI, TUI, and structured actions.

### 7. Verify recovery
Create another order after rollback.

Expected:
- order completes successfully;
- previous known-good artifact remains active;
- previous embedded config remains active;
- no manual config repair or component restart is required.

### 8. Demonstrate process recovery and reconstruction

From the same application console:

```text
Application > Run resilience scenario
Cluster > Restart cluster
```

Expected:
- Inventory moves from its failed Grovlet to a surviving Grovlet and a new
  order completes;
- the application binary reconstructs the deployment after every Grovlet is
  restarted, using durable Grove state;
- the console and the Web status pane return to `healthy` with the known-good
  artifact still active.

## Headline demo contract
The headline human interaction is one executable plus TUI navigation:

```bash
./bin/groveshop
```

```text
Deployments > New rollout > configs/acme.yaml
Deployments > New rollout > configs/acme-broken.yaml
```

Human-facing docs should not replace these contextual TUI flows with generic flag trees.

Automation uses the same application binary and underlying action registry:

```bash
./bin/groveshop action rollout.start --config configs/acme.yaml
./bin/groveshop action rollout.start --config configs/acme-broken.yaml
./bin/groveshop action cluster.status
```

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
