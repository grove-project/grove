# Security

Grove treats secrets as a runtime concern, not application data.

The guiding rule is:

> **Materialize secrets as late as possible, as close to their point of use as possible, for the shortest possible lifetime. Prefer never materializing them in application memory at all.**

## Secure endpoints instead of secret delivery

Applications should normally use their existing protocol libraries rather than asking Grove for credentials.

For example, an application continues to use `database/sql`, pgx, Redis, Kafka, an HTTP client, or another native client. Grove exposes a local protocol-compatible endpoint through the Grovlet:

```text
application
    │
    │ normal PostgreSQL protocol
    ▼
local Grovlet proxy
    │
    ├─ authenticate workload identity
    ├─ authorize access to orders-db
    ├─ obtain a short-lived credential
    ├─ authenticate upstream
    └─ discard the credential
    │
    ▼
database
```

The application receives an authenticated transport, not the credential used to create it. Existing drivers, ORMs, connection pools, transactions, prepared statements, migrations, and tracing continue to work normally.

The same model can extend beyond databases. Grove can inject HTTP authorization, hold mTLS private keys, authenticate NATS or Kafka connections, and proxy other protocols without exposing credentials to application code.

The connection itself effectively becomes the capability.

### Why not return a secret?

Once secret bytes enter arbitrary Go application memory, Grove can no longer reliably control their lifetime. Values may be copied, converted to strings, retained by libraries, logged, included in diagnostics, or kept alive by the runtime.

Grove therefore prefers:

```text
capability -> Grovlet proxy -> authenticated connection
```

over:

```text
secret -> application variable -> client library
```

A raw secret-materialization API may exist as an explicit escape hatch for integrations that cannot be proxied, but it must not be the normal developer experience.

## Trust hierarchy

Grove separates application, node, and workload identity.

```text
Deployment signing key
        │
        ▼
Signed application binary
        │
        ▼
Grovlet verifies + launches
        │
        ├──── short-lived node identity
        │
        ▼
Ephemeral workload identity
        │
        ▼
Capability authorization
        │
        ▼
Grovlet secure endpoint
        │
        ▼
Secret materialized only at final boundary
```

Each step narrows both authority and lifetime.

## Signed application identity

A Grove application binary may contain a signed identity manifest. It contains no secret material and should be safe to disclose with the binary.

Conceptually:

```text
application: groveshop
build:       sha256:8f31...
version:     1.4.2
services:
  - orders
  - payments
  - frontend
signature:
  issuer: acme-production
  sig: ...
```

The signature proves that the deployment authority approved that exact artifact as the named application/build. Possessing a copy of the binary does not provide the signing key and therefore does not permit creation of another trusted artifact.

The signed identity answers **what is allowed to run**. It is not a runtime credential.

A manifest alone does not prove that the corresponding executable is actually running. The Grovlet is therefore the normal trusted execution boundary: it verifies the artifact, launches the worker itself, and associates that verified artifact with the resulting worker identity. Workers must not be allowed to self-assert application identity.

Because Grove can embed customer configuration into the application artifact, code, configuration, and application identity can be covered by the same signed artifact. Changing embedded configuration changes the artifact identity.

## Node enrollment

Nodes enroll into a Grove cluster; they do not possess a permanent shared cluster password.

Bootstrap and steady-state identity are deliberately separate.

### Existing platform identity

Where available, a Grovlet can prove an identity already supplied by its environment, such as a cloud workload identity, Kubernetes ServiceAccount, or hardware-backed device identity. The cluster exchanges that proof for a Grove node identity.

### One-time enrollment

Automated provisioning can use a short-lived, single-use enrollment token. The token should be an opaque high-entropy value. The cluster stores only the information required to validate it, preferably keyed by a cryptographic hash, together with constraints such as cluster, site, expiration, allowed node attributes, and maximum uses.

```text
provisioner
    │ one-time enrollment token
    ▼
new Grovlet
    │ outbound TLS connection
    ▼
cluster enrollment service
    ├─ validate token
    ├─ validate constraints
    ├─ atomically consume token
    └─ issue node identity
```

The enrollment token is invalid after successful use and never becomes a runtime credential.

### Interactive enrollment

For manually installed bare-metal or customer-edge nodes, Grove can avoid transferable bootstrap secrets entirely.

```text
node generates private key locally
        ↓
node connects outbound and becomes pending
        ↓
operator approves the pending node/public key
        ↓
cluster issues node identity
```

The node private key never leaves the machine. Hardware-backed non-exportable keys should be used where available.

All enrollment mechanisms converge on the same steady state: a node-owned key plus automatically renewed, short-lived Grove identity.

## Workload identity

A worker's runtime identity is derived by Grove rather than supplied by application code. It can bind together:

```text
application: groveshop
service:     payments
build:       sha256:8f31...
node:        edge-17
instance:    91ac...
```

This identity is ephemeral and tied to the actual worker launched by the Grovlet.

When a worker terminates, migrates, or is replaced, its runtime identity and associated capabilities expire with it.

## Capability authorization

Possessing a valid node identity does not authorize access to every cluster secret.

Authorization should combine the verified workload and node context:

```text
verified application/build
        +
service identity
        +
node identity
        +
placement/environment constraints
        ↓
capability authorization
```

For example, `groveshop/payments` may be permitted to use the `stripe` secure endpoint only while running as an approved build on an eligible node. A service that requires a customer-LAN resource can additionally require placement on a node whose placement validation succeeded for that environment.

Capabilities should describe operations or resources rather than credentials. The application is authorized to *use* `orders-db` or `stripe`; it is not authorized to possess their passwords or private keys.

## NATS role

NATS is useful for Grove's authenticated control and coordination plane, but Grove should avoid distributing raw secrets through ordinary NATS messages, KV, or JetStream merely because those channels are secured.

NATS can carry requests and identity context such as:

```text
service = payments
instance = 91ac...
node = edge-17
capability = stripe
operation = http
```

The actual secret should remain at the narrowest trusted boundary that needs it.

## Materialization escape hatch

Some third-party APIs inevitably require application code to receive secret bytes. Grove may provide an explicit materialization primitive for these cases, with deliberately visible semantics rather than making it the convenient default.

When materialization is unavoidable, Grove should minimize exposure using short leases, no caching by default, mutable byte buffers rather than strings where possible, locked/non-swappable memory where supported, diagnostic and log redaction, and immediate buffer zeroing after use.

These measures reduce exposure but do not provide a hard guarantee once bytes have entered arbitrary application code.

## Future hardening

The initial security boundary trusts the Grovlet and host sufficiently to verify and launch application workers. Higher-assurance deployments can strengthen this model with TPM-backed node keys, measured boot, remote attestation, or trusted execution environments.

Those mechanisms should strengthen the same identity and capability model rather than introduce a separate application-facing security abstraction.

## Design principles

1. **Secrets are runtime infrastructure, not application configuration.**
2. **Prefer capabilities and authenticated transports over credential delivery.**
3. **Application code should keep using native protocol libraries.**
4. **Nodes enroll; they do not share a permanent cluster password.**
5. **Application artifacts contain signed public identity, never secret identity material.**
6. **Grovlets verify and launch workers; workers do not self-assert identity.**
7. **Every transition should narrow authority and shorten credential lifetime.**
8. **NATS transports authorization context, not long-lived application secrets.**
9. **Raw secret materialization is an explicit compatibility escape hatch.**
10. **Security features should preserve Grove's low-intrusion developer experience.**
