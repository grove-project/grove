# Task 016 — Managed component lifecycle

Status: TODO
Depends on: 015

## Goal
Make Grovlet responsible for starting, stopping and reporting hosted component lifecycle while enforcing service placement eligibility.

## Scope
States such as starting, healthy, stopping, stopped, failed.

Before starting a component, the Grovlet must verify that the service is currently placement-eligible on that node. A service with no placement validator is eligible by default. A service whose validator fails must not transition into `starting` or `healthy` on that Grovlet.

If eligibility cannot be established, lifecycle status should expose a useful reason rather than reporting a generic startup failure.

## Hard invariant
A Grovlet must never start or restart a service that does not pass its placement validation.

This applies to initial start, explicit restart, and later recovery paths that reuse the lifecycle primitive.

## Out of scope
Automatic recovery, persistence, rolling upgrades, general scheduler scoring, and dynamic relocation after eligibility changes.

## E2E
Start an unrestricted component and verify healthy. Start a placement-restricted component on an eligible Grovlet and verify healthy. Attempt to start it on an ineligible Grovlet and verify it never starts and exposes the placement reason. Then stop/start the eligible instance again and verify the application flow.

## Done
`go test ./...` passes.
