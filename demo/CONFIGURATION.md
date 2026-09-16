# Grove Shop Embedded Configuration Demo

## Purpose
The MVP must visibly prove Grove's embedded customer-configuration model and show that configuration participates in the same immutable deployment and rollback lifecycle as application code.

## Good configuration
Use a simple customer-specific YAML file such as:

```yaml
customer:
  name: Acme Retail

inventory:
  reservation_buffer: 100
```

The exact schema may evolve, but at least one value must influence normal application behavior and be visible through the running application or status UI.

## Broken configuration
The demo also includes a deliberately invalid configuration:

```yaml
customer:
  name: Acme Retail

inventory:
  reservation_buffer: -1
```

Inventory must treat a negative reservation buffer as invalid and fail deterministically during candidate startup/initialization.

The failure may be implemented as a returned fatal startup error or a process/component crash depending on the component model available at that stage. The important observable behavior is that Grove marks the candidate unhealthy and rejects it.

Do not implement a hidden `crash=true` switch as the primary scenario. The failure should come from a plausible invalid application configuration value.

## Artifact semantics
Configuration is embedded after compilation into a reserved region of the Grove application binary/artifact.

Conceptually:

```text
go build
   |
   v
base Grove Shop binary
   |
   +-- embed configs/acme.yaml
   |       -> Artifact A
   |
   `-- embed configs/acme-broken.yaml
           -> Artifact B
```

Each resulting artifact is immutable for deployment purposes.

Artifact identity must distinguish the good and broken artifacts even when the application code version is identical.

## Rollback semantics
Rollback always means returning to the previous complete known-good artifact:

```text
Artifact A
app version N + UI + config A
       |
       | active
       v
Artifact B
app version N + UI + config B
       |
       | candidate fails
       v
reject B
retain/restore Artifact A
```

Do not repair Artifact B by mutating its config in place. Do not copy only the previous config value back into the candidate.

## Application console workflow
The developer experience is one application binary, not a separately required
Grove CLI:

```bash
go build -o ./bin/groveshop ./cmd/grovlet
./bin/groveshop
```

```text
Deployments > New rollout > configs/acme.yaml
Deployments > New rollout > configs/acme-broken.yaml
```

For scripts, CI, and automated tests, invoke the same registered actions from
that binary:

```bash
./bin/groveshop action rollout.start --config configs/acme.yaml
./bin/groveshop action rollout.start --config configs/acme-broken.yaml
```

The action form reaches the same rollout implementation as the contextual TUI
selection. Lower-level config compile/embed/extract commands remain inspection
and test tools, not part of the headline demo.

## Required observability
The Cluster Status UI should make the configuration lifecycle visible through stable metadata, for example:
- active artifact identity,
- candidate artifact identity,
- active config revision/hash/name,
- candidate config revision/hash/name,
- candidate component failure,
- rollback reason,
- final active known-good artifact.

The UI should not display secrets or arbitrary raw configuration contents.
