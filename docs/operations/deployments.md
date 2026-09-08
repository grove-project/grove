# Deployments and Recovery

A Grove release is one versioned application artifact. Code, runtime behavior, and customer configuration move through the same deployment lifecycle.

```bash
$ grove deploy ./shop

Version    v0.8.3
Config     production-43
State      validating
```

A healthy rollout should make the transition visible:

```text
current v0.8.2
    ↓
stage v0.8.3
    ↓
run health + E2E validation
    ↓
approve / cut over
    ↓
retain v0.8.2 for rollback
```

> Commands and output here describe the intended Grove experience while the MVP is still evolving.

## Configuration is a deployment

Grove does not mutate production configuration underneath a running cluster. A configuration change creates a new immutable artifact, even when application code is unchanged.

```bash
$ grove config embed \
    --binary ./shop \
    --config production.yaml \
    --output ./shop-production
```

The artifact carries both code identity and configuration identity so operators can determine exactly what is running.

A broken config should look like an understandable deployment failure, not a mystery:

```text
Config       production-43
Observed     orders crash-looping
Cause        invalid orders.max_batch_size
Action       rollback to production-42
Result       recovered
```

## Safe upgrades

New and old application versions remain isolated by default. Side-by-side versions exist for validation and rollback readiness, not as a requirement for permanent mixed-version compatibility.

The operational contract is:

> **prove → approve → cut over → retain rollback → retire**

Upgrade state and recovery metadata should be durable so interruption does not leave the deployment in an unknown phase.

## State migrations

Persisted representation changes use adjacent migration adapters (`N ↔ N+1`). Grove may compose them across retained versions. Every migration hop is validated by the target version's own E2E contract before proceeding.

Downgrade availability and downgrade losslessness are different properties; Grove should surface known destructive effects before execution.

## Edge deployments

Cloud and customer-edge nodes can participate in the same logical Grove cluster. Edge Grovlets initiate outbound connectivity; normal operation should not require inbound Internet ports at the customer site.

Cloud and edge should normally run the same application/runtime version. Edge rollout state, connectivity, local health, and rollback readiness should be visible through the same Grove operational model.