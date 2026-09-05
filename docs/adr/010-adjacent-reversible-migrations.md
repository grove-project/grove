ADR-010 — Adjacent Reversible Migrations and Version-Bound E2E

Status  
Accepted

Context  
Grove aims to keep a distributed application on one coherent binary version across cloud and customer edge. Persisted data makes rollback harder than binary replacement: a newer release may change schemas, serialized records, KV values, object metadata, local edge state, or other durable representations in ways an older binary cannot understand.

Requiring every release to remain compatible with every historical data representation would recreate the compatibility burden Grove is designed to avoid. At the same time, rollback and upgrades must remain practical even when a customer is several releases behind.

Decision  
Every release that changes persisted representation must provide the adjacent two-way migration boundary between the previous release and itself: N-1 ↔ N. Developers are not required to implement arbitrary N ↔ M adapters.

Grove retains/composes these adjacent adapters as a migration chain. A move between arbitrary retained versions is planned as a sequence of adjacent transitions. Both upgrade and downgrade paths use the same model.

Each release also carries the E2E suite compiled with that release and its version-specific correctness/SLA expectations. After every migration hop Grove boots the target binary and runs the target release's E2E contract. A hop is accepted only after that target version proves it can operate correctly on the migrated state; successful execution of the migration function alone is insufficient.

Downgrade migrations may be lossy. Grove distinguishes migration possible, reversible, and lossless. Known destructive effects should be surfaced before execution, but loss does not automatically invalidate a downgrade if the target version's E2E contract succeeds on the resulting state.

Consequences  
• Developer migration complexity remains bounded to one adjacent version boundary per release.  
• Grove can compose adjacent adapters to migrate forward or backward across many releases.  
• Old binaries do not need native knowledge of every future schema, and new binaries do not need direct migration logic for every historical schema.  
• E2E tests become semantic validators for persisted-state transitions, not only pre-release application tests.  
• Each historical release must remain an executable/self-describing artifact containing the code and correctness contract required to validate that version.  
• Migration chains can be exercised in CI, including forward migration, per-hop E2E, reverse migration, and per-hop older-version E2E.  
• Grove must checkpoint migration progress durably so multi-hop transitions can resume or fail at a known boundary.  
• Data loss during downgrade must be observable and communicated; rollback availability must not be presented as synonymous with lossless rollback.

Upgrade Interaction  
Two application versions may exist side-by-side for candidate validation and rollback readiness, but ordinary production traffic is not split between them by default. One coherent application version owns production traffic/state at a time. Grove validates the candidate, obtains required approval, performs a coordinated cutover, and keeps the previous release available for rollback until retirement policy allows garbage collection.

Alternatives Considered  
• Require every release to read/write all historical schemas — rejected because compatibility burden grows indefinitely.  
• Require direct migration adapters between every pair of versions — rejected because the number of adapters grows combinatorially.  
• Treat a successful schema/data migration as sufficient validation — rejected because syntactically valid migrated state may still violate application semantics.  
• Depend on production canary traffic split between versions — rejected as the default because mixed-version execution creates cross-version call, side-effect, and state-ownership complexity.

Related Docs  
Grove — System Architecture  
Deployment  
Testing  
ADR-001 — Single Versioned Binary  
ADR-005 — Version Isolation by Default  
