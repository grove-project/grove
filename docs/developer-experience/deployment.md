Deployment

Status: Draft / evolving

Goal  
Deploy the same Grove application artifact from development into standalone or clustered environments with minimal additional packaging.

Deployment Model  
A Grove release is a versioned application artifact. Target machines run Grove, join the cluster, and receive the application artifact. Grove then places and supervises application work according to cluster state and policy.

Desired Experience  
• Build one release artifact.  
• Copy or distribute it to the target environment.  
• Join nodes to the Grove cluster.  
• Declare or activate the desired application version.  
• Let Grove place workers/services and expose ingress.  
• Observe health and rollout state through the CLI.

Upgrade Direction  
New and old application versions remain isolated by default. Grove coordinates rollout and ingress transition without assuming mixed-version service compatibility.

Deployment Environments  
• Single-machine local or edge deployment.  
• Standalone multi-node Grove cluster.  
• Customer/on-prem environments with constrained infrastructure.  
• Future hosting inside existing Kubernetes environments where useful.

Open Questions  
• Artifact distribution mechanism.  
• Cluster bootstrap and node-join credentials.  
• Stable ingress endpoint discovery.  
• Upgrade/rollback state machine.  
• Configuration and secrets delivery.  
• How Kubernetes-hosted Grove should integrate with surrounding platform primitives.

Edge / Cloud Deployment  
Grove deployments may span cloud and customer edge networks as one logical cluster. Placement policy can require selected components to remain at a customer's edge while other components run in cloud. Edge Grovlets establish outbound-only connectivity to the rest of the Grove cluster; Grove should not require inbound Internet ports on the customer network.

Cloud and edge should normally run the exact same Grove application/runtime binary version. Version skew is tolerated only as a bounded upgrade state. Customer approval and maintenance constraints are handled by safe staging and activation rather than by designing permanent cross-version compatibility.

Safe Upgrade Model  
Grove uses side-by-side versions for validation and rollback readiness, not normal production traffic splitting. The current version remains authoritative while the candidate is staged and validated. Grove runs the candidate's built-in E2E contract and operational checks, presents the evidence, and requests approval for a coordinated cutover. At ordinary steady state, production traffic belongs to one coherent application version.

After cutover, the previous release remains locally available and rollback-ready for a configured window. Upgrade state, checkpoints, binaries, and required recovery metadata are durable so interruption or power loss cannot leave the deployment in an unknown phase. Retirement/garbage collection of the old release happens only after the new version is accepted and the rollback policy permits it.

State Migration and Rollback  
Every release that changes persisted representation provides an adjacent two-way migration adapter for N-1 ↔ N. Grove composes adjacent adapters to migrate between arbitrary retained versions. Downgrades may lose information introduced by newer versions; migration availability and losslessness are separate properties and Grove should surface known destructive effects before execution.

Every migration hop is validated by the target release itself: migrate to the adjacent target state, boot the target binary, run the E2E suite compiled with that version, validate its declared SLA/correctness expectations, checkpoint the successful hop, and continue. On failure Grove stops at the failing transition and preserves enough state to diagnose or reverse/restore according to the available path.

The operational contract is therefore: prove → approve → cut over → retain rollback → retire old version.

Immutable Customer Configuration  
Grove treats customer configuration as part of the immutable deployment artifact rather than as mutable files distributed independently across machines. The configuration contract—types/schema, defaults, validation rules, documentation, and version migration logic—is compiled into the canonical application binary. Customer-, environment-, site-, and deployment-specific configuration values are attached to that already-built binary as a post-build configuration bundle.

This preserves one canonical executable code image while allowing Grove to produce customer-specific artifacts without recompiling application code. Artifact identity must distinguish the executable/code digest, configuration revision and digest, and final artifact digest so Grove can prove that two artifacts contain identical code even when their customer configuration differs.

A deployed Grove cluster does not mutate configuration in place. A configuration change produces a new immutable artifact, even when the executable code is unchanged. Grove then transitions from the current cluster/release to the new artifact using the same deployment, validation, migration, cutover, and rollback machinery used for a software upgrade. In other words, configuration management is deployment management rather than a separate runtime mutation system.

This deliberately removes the normal production model of loose configuration files, ConfigMaps, copied YAML, or independently drifting machine configuration. The artifact itself carries the approved customer configuration baseline. If two nodes run the same artifact digest, they run the same executable code and begin from the same configuration.

Example identity:  
Code version: v1.8.0  
Code digest: sha256:<code>  
Customer: Acme  
Config revision: r43  
Config digest: sha256:<config>  
Artifact digest: sha256:<artifact>

A config-only transition therefore looks like:  
Artifact A = code v1.8.0 + Acme config r42  
Artifact B = same code v1.8.0 + Acme config r43  
Grove stages Artifact B, runs the target artifact's E2E/SLA checks, coordinates cloud and edge transition, cuts over only after validation/approval, and retains Artifact A for rollback according to policy.

Operational Invariants  
• Running Grove artifacts are immutable; configuration never changes underneath a running cluster.  
• Every production configuration change creates a new artifact and an observable deployment event.  
• Customer configuration is attached after build; Grove does not require recompiling the application for each customer.  
• Code identity and artifact identity are separate and independently verifiable.  
• Configuration rollout inherits Grove's existing safe-upgrade, E2E validation, state migration, edge rollout, and rollback semantics.  
• Production nodes should not depend on loose customer configuration files for normal operation.  
• Given an artifact digest, Grove can identify the exact code and configuration combination used to reproduce a deployment.

Embedded Configuration Format and CLI  
At build time, the Grove binary reserves a fixed-capacity configuration section. The executable is built once with this empty/reserved region; customer configuration is subsequently written only into that region. Post-build embedding must not resize or restructure the executable.

The Grove CLI accepts human-editable YAML as the authoring format. During embedding it validates and decodes the YAML into the application's typed configuration model, serializes the resulting configuration using Go gob, compresses the serialized payload, calculates integrity metadata, and writes the payload into the reserved configuration section.

Conceptual flow:  
YAML → typed validation/decode → gob → compression → reserved binary config section

Example:  
grove config embed --binary grove-v1.8.0 --config acme.yaml --output grove-v1.8.0-acme

The embedded section should contain a versioned header in addition to the payload. At minimum the header should identify the Grove config format/encoding version, encoded payload length, uncompressed length, compression format, config revision/digest, and integrity/signature information where applicable. The encoding version is explicit so the artifact format can evolve beyond the initial gob-based implementation without ambiguity.

Reserved Capacity  
The config section has a capacity chosen when the executable is built. Embedding succeeds only when the compressed configuration and required metadata fit inside that capacity. The embed command must not silently enlarge the binary section. If the configuration does not fit, Grove reports the required size and requires rebuilding the canonical executable with a larger config-section allocation, for example:  
grove build --config-capacity 2MiB

This preserves the invariant that post-build customer customization modifies only bytes inside a deliberately reserved data region and does not alter executable code or layout.

Extraction and Inspection  
Configuration embedding is intentionally reversible. The CLI can extract the effective embedded configuration from an existing Grove binary and return it to the human-editable YAML representation:  
grove config extract --binary grove-v1.8.0-acme --output acme.yaml

Extraction reads the reserved section, validates its header/integrity, decompresses the payload, decodes the gob configuration according to the embedded format version/application configuration model, and emits YAML. This makes the deployed binary itself the source of truth without making its configuration opaque or inaccessible to operators.

The CLI should also support metadata-only inspection without extracting the full configuration:  
grove config inspect --binary grove-v1.8.0-acme

Inspection should expose information such as config revision/digest, encoding and compression format, encoded size, reserved capacity/remaining capacity, and integrity/signature status.

Artifact Hashing  
Grove should distinguish a code digest that excludes or normalizes the reserved configuration section from the complete artifact digest. This allows two customer artifacts to prove they contain identical executable code while still having distinct configuration and artifact identities.

The resulting operational model is round-trip and self-contained: YAML is used to author configuration; Grove compiles it into a compact typed embedded representation; the resulting binary is the deployment source of truth; and Grove can always inspect or extract that configuration back into an operator-friendly form.  
