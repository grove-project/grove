# Task 024 — Embedded customer configuration

Status: TODO
Depends on: 023

## Goal
Embed customer configuration into the Grove application binary.

## Scope
YAML input -> chosen internal representation -> compression -> reserved binary region; extraction; clear overflow failure.

## Out of scope
Live config mutation, distributed config service, automatic rebuild on overflow.

## Tests
Encode/decode and size bounds. E2E build artifact, embed config, extract it, deploy it, verify app observes config.

## Done
`go test ./...` passes.