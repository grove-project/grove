# Task 004 — Local multi-Grovlet cluster harness

Status: TODO
Depends on: 003

## Goal
Let a Go test launch N independent Grovlet processes on one host.

## Scope
`NewCluster`, node count, Start, Node(i), WaitAllReady, Stop, DumpDiagnostics; unique IDs, ports and temp state per node.

## Out of scope
Nodes do not need to know each other yet. Do not add membership.

## E2E
Launch 3 real Grovlets, prove all alive, kill one, prove others remain alive, clean up automatically.

## Done
`go test ./...` passes.