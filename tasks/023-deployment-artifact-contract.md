# Task 023 — Deployment artifact contract

Status: TODO
Depends on: 019

## Goal
Define the MVP Grove application artifact contract.

## Scope
Application identity/version, component declarations, and minimal entrypoint/runtime metadata needed to start components.

## Out of scope
OCI registry, signing, remote distribution, Firecracker images.

## E2E
Build reference artifact during `go test`, inspect metadata, deploy it to local cluster, verify flow.

## Done
`go test ./...` passes.