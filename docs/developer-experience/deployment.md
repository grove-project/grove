Deployment

Status: Draft / evolving

Goal  
Deploy the same Grove application code from development into standalone or clustered environments while keeping deployment configuration explicit, immutable, and inspectable.

Deployment Model  
A Grove release is a versioned binary artifact containing application code, Grove runtime, and an embedded configuration section. A running artifact never mutates its configuration. A configuration change creates a new artifact and therefore a new rollout.

Different node classes may use different configured variants of the same code binary. For example, one artifact may embed `node.zone: cloud` while another embeds `node.zone: edge`. Grove does not infer these authoritative placement-domain facts from IP addresses or network heuristics; the running Grovlet reads them from its embedded configuration.

Desired Experience  
• Build one canonical executable/code image.  
• Embed the configuration for each required deployment/node class.  
• Run those immutable artifacts as Grove nodes.  
• Let Grove place workers/services according to hard eligibility and policy.  
• Roll configuration or software changes out as successor artifacts over the existing Grove mesh.  
• Observe health, handoff, failure reasons, and rollback through the CLI.

Core Invariant  
**Grove never mutates the configuration of a running binary. A configuration change produces a new immutable artifact, and the running cluster rolls that artifact out through binary handoff.**

Node Classes and Placement Facts  
Cluster and node identity facts that are configuration choices belong in the embedded configuration. A simple example is:

```yaml
cluster:
  name: production
node:
  zone: cloud
```

and a second configured artifact may contain:

```yaml
cluster:
  name: production
node:
  zone: edge
```

Both artifacts can contain identical executable/application code while having different configuration and final artifact digests. `zone` is therefore an authoritative immutable fact of that running artifact, not something Grove guesses from reachability. Live environmental probes remain useful for facts such as customer-LAN reachability, hardware presence, or local dependencies.

A placement validator may combine both kinds of truth. For example, a cloud-only service can require `node.zone == cloud`; an edge integration may require `node.zone == edge` and also validate that a particular LAN endpoint is currently reachable.

Binary Handoff Rollout  
The current Grove cluster is the transport and bootstrap mechanism for its successor. Grove does not require an external SSH/Ansible-style deployment path for normal upgrades once a cluster exists.

For each target node the current Grovlet:

1. receives or obtains the successor artifact through the Grove deployment path,
2. verifies artifact identity/integrity and embedded configuration,
3. checks cluster and node-class compatibility,
4. persists the candidate locally,
5. launches the new binary beside the old binary,
6. waits for the new Grovlet to join the existing mesh and become healthy,
7. hands node/service responsibility to the new process,
8. exits the old process only after successful handoff.

The old process remains authoritative until the successor proves it can take ownership. If transfer, verification, startup, join, validation, or handoff fails, Grove terminates/rejects the candidate and the old process continues serving. This makes rollback readiness part of the handoff protocol rather than a best-effort recovery after destroying the previous runtime.

A normal same-class rollout looks like:

```text
cloud-v1 -> cloud-v2
edge-v1  -> edge-v2
```

A successor whose immutable node class does not match the target node should be rejected by the handoff contract. Changing a machine's fundamental node class is explicit reprovisioning, not an accidental side effect of a rollout.

Rollout Observability  
Rollout progress is durable cluster state and should be inspectable from any healthy Grove CLI connection. Useful phases include:

```text
pending -> transferring -> verifying -> starting -> joining -> handoff -> healthy
                                                           \-> failed
```

The CLI should show per-node old/new artifact identity, target node class, current phase, health, timestamps, and actionable failure reason. Operators should be able to distinguish artifact/config incompatibility, failed startup, failed placement validation, join failure, and health failure.

The CLI boundary is deliberate: **the CLI may inspect configuration and control/observe rollouts; it does not edit the configuration of running nodes.**

Upgrade Direction  
New and old application versions remain isolated by default. Grove coordinates rollout and ingress transition without assuming mixed-version service compatibility.

Deployment Environments  
• Single-machine local or edge deployment.  
• Standalone multi-node Grove cluster.  
• Customer/on-prem environments with constrained infrastructure.  
• Future hosting inside existing Kubernetes environments where useful.

Edge / Cloud Deployment  
Grove deployments may span cloud and customer edge networks as one logical cluster. Cloud and edge can use configured variants of the same canonical executable: the code remains the same while immutable embedded node configuration identifies the node class/zone. Edge Grovlets establish outbound-only connectivity to the rest of the Grove cluster; Grove should not require inbound Internet ports on the customer network.

Cloud and edge should normally run the same application/runtime code version. Their final artifact digests may differ because their embedded node configuration differs. Version skew is tolerated only as a bounded upgrade state.

Safe Upgrade Model  
Grove uses side-by-side binaries for validation and rollback readiness, not normal production traffic splitting. The current process remains authoritative while the candidate is transferred, started, joined, and validated. Grove runs the candidate's built-in E2E contract and operational checks where applicable and coordinates ownership/cutover only after the candidate is healthy.

After handoff, the previous release remains locally available and rollback-ready for a configured window. Upgrade state, checkpoints, binaries, and required recovery metadata are durable so interruption or power loss cannot leave the deployment in an unknown phase. Retirement/garbage collection of the old release happens only after the new version is accepted and rollback policy permits it.

State Migration and Rollback  
Every release that changes persisted representation provides an adjacent two-way migration adapter for N-1 ↔ N. Grove composes adjacent adapters to migrate between retained versions. Downgrades may lose information introduced by newer versions; migration availability and losslessness are separate properties and Grove should surface known destructive effects before execution.

Every migration hop is validated by the target release itself: migrate to the adjacent target state, boot the target binary, run the E2E suite compiled with that version, validate its declared SLA/correctness expectations, checkpoint the successful hop, and continue. On failure Grove stops at the failing transition and preserves enough state to diagnose or reverse/restore according to the available path.

The operational contract is therefore: transfer -> verify -> start -> join -> prove -> handoff -> retain rollback -> retire old version.

Immutable Customer Configuration  
Grove treats customer and cluster configuration as part of the immutable deployment artifact rather than as mutable files distributed independently across machines. The configuration contract—types/schema, defaults, validation rules, documentation, and version migration logic—is compiled into the canonical application binary. Customer-, environment-, site-, deployment-, and node-class-specific values are attached to that already-built binary as a post-build configuration bundle.

This preserves one canonical executable code image while allowing Grove to produce configured artifacts without recompiling application code. Artifact identity must distinguish executable/code digest, configuration revision/digest, and final artifact digest.

A deployed Grove process does not mutate configuration in place. A config-only transition from r42 to r43 is a rollout from Artifact A to Artifact B through the same mesh/handoff machinery used for a software upgrade.

Operational Invariants  
• Running Grove artifacts are immutable; configuration never changes underneath a running process.  
• Every production configuration change creates a new artifact and an observable rollout event.  
• Node class/zone comes from immutable embedded configuration, not network auto-detection.  
• Customer/cluster configuration can be attached after build; Grove does not require recompiling application code for each variant.  
• Code identity, configuration identity, and artifact identity are separate and independently verifiable.  
• The current Grove mesh bootstraps the successor artifact.  
• The old process remains authoritative until side-by-side handoff succeeds.  
• The CLI inspects configuration and observes/controls rollout; it does not mutate running configuration.  
• Given an artifact digest, Grove can identify the exact code and configuration combination used to reproduce a deployment.

Embedded Configuration Format and CLI  
At build time, the Grove binary reserves a fixed-capacity configuration section. The executable is built once with this empty/reserved region; configuration is subsequently written only into that region. Post-build embedding must not resize or restructure the executable.

The Grove CLI accepts human-editable YAML as the authoring format. During embedding it validates and decodes YAML into the application's typed configuration model, serializes the configuration using Go gob, compresses the payload, calculates integrity metadata, and writes the payload into the reserved section.

Conceptual flow:  
YAML -> typed validation/decode -> gob -> compression -> reserved binary config section

Example:  
`grove config embed --binary grove-v1.8.0 --config cloud.yaml --output grove-v1.8.0-cloud`

The embedded section should contain a versioned header including format/encoding version, payload lengths, compression, config revision/digest, and integrity/signature information where applicable.

Reserved Capacity  
Embedding succeeds only when the compressed configuration and metadata fit in the capacity chosen at build time. The CLI must not silently enlarge the section. If it does not fit, Grove reports the required size and requires rebuilding the canonical executable with larger capacity.

Extraction and Inspection  
Configuration embedding is reversible for investigation and artifact construction. The CLI can extract configuration from an artifact and inspect metadata:

```text
grove config extract --binary grove-v1.8.0-cloud --output cloud.yaml
grove config inspect --binary grove-v1.8.0-cloud
```

These commands operate on artifacts; they do not mutate a running node. Runtime inspection should expose the effective embedded configuration/config identity of each node without offering a `set` operation.

Artifact Hashing  
Grove distinguishes a code digest that excludes/normalizes the reserved configuration section from the complete artifact digest. This lets cloud and edge artifacts prove identical executable code while retaining distinct configuration and artifact identities.

Placement Eligibility During Deployment  
Grove separates service eligibility from scheduling preference. A service may optionally include placement-validation logic through the SDK. Every Grovlet evaluates that logic against its local facts/environment, and only Grovlets that pass are eligible.

A service with no validator is eligible on every Grove node. Validators may consume immutable Grove facts such as configured node zone as well as live environmental checks. Failed placement validation is a hard runtime requirement; scheduling policy only optimizes among eligible nodes.
