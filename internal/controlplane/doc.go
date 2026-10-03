// Package controlplane is Grove's control-plane domain: the records the
// cluster agrees on (membership, desired deployments, artifacts and rollouts,
// service placement, handler placement and exclusive capability leases) and
// the rules that decide how they may change.
//
// It says what Grove means, not how Grove stores or transports it. Every
// function here is pure: no NATS, no JetStream, no clock reads, no I/O. The
// System NATS adapter (internal/systemnats) persists these records in
// JetStream/KV, watches them, and applies these rules before each
// compare-and-set write. Storage details such as KV revisions stay in the
// adapter; fencing is defined here by epoch, holder and lease time.
//
// The dependency direction is fixed and guarded by boundary_test.go:
//
//	controlplane (domain)  <--  systemnats (NATS adapter)  <--  runtime, cmd/grove
package controlplane
