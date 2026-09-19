package main

import "testing"

// One E2E owns the complete Grove Shop demonstration: good deployment,
// business traffic, failure recovery, durable restart, rejected candidate,
// rollback, then the topology-explicit two-worker Delve/DAP debugging flow.
//
// The debugging walkthrough starts a fresh application console because its
// documented five-node topology is a separate deployment shape from the
// three-node rollout demo. Both phases still use real Grove Shop binaries,
// Grovlet processes, production-shaped transport, and condition-based waits.
func TestGroveShopFullDemoFlow(t *testing.T) {
	t.Log("phase 1: rollout, orders, recovery, restart, rejected candidate, and rollback")
	runGroveShopLifecycleDemo(t)
	t.Log("phase 2: five-node topology, two Delve/DAP sessions, breakpoints, and recovery")
	runGroveShopDebuggingDemo(t)
}
