# Task 001 — Repository skeleton

Status: TODO

## Goal
Establish a minimal Go repository where `go test ./...` succeeds from a clean checkout.

## Scope
Create the Go module, `cmd/grovlet`, only immediately-needed package structure, and a meaningful starter unit test.

## Out of scope
Networking, services, cluster behavior, Raft, configuration framework.

## Tests
Unit test repository wiring. No E2E yet.

## Done
`go test ./...` passes.