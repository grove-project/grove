Testing

Status: Draft / evolving

Goal  
Make realistic end-to-end testing a first-class Grove capability.

Core Idea  
A Grove application should be testable as the same self-contained artifact that is later deployed. Tests should exercise both application logic and meaningful runtime behavior rather than a simplified mock topology.

Testing Modes  
• Unit tests for deterministic application logic.  
• Local integration tests with Grove services running together.  
• End-to-end tests that start the complete Grove application artifact.  
• Failure-injection tests for worker crashes, node loss, delayed messaging, and storage failover.  
• Upgrade tests that validate version isolation and rollout behavior.

Key Principle  
What you test locally is what you ship.

Desired Capabilities  
• Programmatic cluster startup and shutdown for tests.  
• Deterministic health/readiness gates.  
• Test-visible service topology and placement.  
• Fault injection and controlled restarts.  
• Temporary local storage with optional persistence.  
• Easy collection of logs, traces, and runtime state on failure.

Open Questions  
• Exact test harness API.  
• How deterministic placement should be during tests.  
• Whether multi-node tests use processes, containers, VMs, or multiple hosts.  
• Standard fault-injection primitives.

E2E Flows as the Developer Contract  
Developers should focus on describing real application functionality and correctness. Grove should reuse those same E2E flows as the basis for resilience validation rather than asking developers to author a separate chaos-test suite.

The developer defines:  
• Business flow and correctness assertions.  
• Optional SLOs when known.

Grove derives and executes:  
• Relevant worker, service, node, storage, routing, and upgrade failure scenarios.  
• Failure timing based on the actual execution path observed during healthy baseline runs.  
• Repetition and measurement needed to evaluate resilience.

Flow-Aware Resilience Matrix  
A baseline E2E execution reveals which services, storage components, and communication edges participate in the flow. Grove should use this observed path to generate a focused resilience matrix instead of perturbing unrelated parts of the application.

Correctness Before SLO Precision  
Developers should not be required to know exact SLOs before writing useful tests. Grove should support:  
• Functional-only flows, where Grove reports observed latency and availability.  
• Qualitative expectations such as user-facing, high-availability, or eventually-completes.  
• Explicit SLOs when the team is ready to define them.

Grove should help teams discover realistic SLOs from repeated baseline and failure-condition measurements.

Testing Principle  
The developer specifies what must keep working. Grove figures out how to try to break it.

Version-Bound E2E Contracts  
The E2E suite is part of the versioned Grove release and is compiled with the application version it validates. Each release therefore carries its own definition of functional correctness and, where declared, SLA expectations.

This matters during upgrades and rollbacks: after state is migrated to version N, Grove boots binary N and runs the E2E contract embedded in N. It must not judge an older version using tests for features introduced by a newer release.

Migration-Chain Validation  
Persisted-state migrations are adjacent and two-way: developers provide only N ↔ N+1 adapters. Grove can compose these adapters to move between arbitrary retained versions. For a multi-hop migration, every hop is an independent validation boundary:

• Apply the adjacent migration into the target version's state representation.  
• Boot the target version.  
• Run that target version's E2E suite.  
• Validate declared correctness and SLA expectations.  
• Checkpoint success before continuing to the next hop.

The same algorithm applies to downgrade/rollback paths. A downgrade can be lossy and still be operationally valid if the older application's E2E contract succeeds on the migrated state. Grove should distinguish migration possible, reversible, and lossless rather than treating them as the same guarantee.

Release Validation Principle  
A migration function returning successfully proves only that a transformation executed. Grove accepts a migration hop only when the target application proves it can operate correctly on the resulting state.

This allows CI and pre-production testing to exercise complete upgrade and rollback chains, including upgrade → E2E → downgrade → older-version E2E, before a release is considered safe for customer deployment.  
