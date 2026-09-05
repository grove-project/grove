# Task 011 — Node identity and advertised endpoint

Status: TODO
Depends on: 009

## Goal
Make node identity explicit and independent from host identity.

## Scope
Stable process-lifetime node ID, advertised transport endpoint, readiness state; validate identity configuration.

## Out of scope
Discovery, membership consensus, persistent machine identity.

## E2E
Multiple Grovlets on the same host remain distinct and addressable.

## Done
`go test ./...` passes.