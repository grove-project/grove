# Task 031 — Final MVP lifecycle E2E

Status: TODO
Depends on: 020, 024, 028, 030

## Goal
Create one automated regression test demonstrating the full Grove MVP thesis.

## E2E
Through Go testing + `grovetest`: build reference artifact; embed config; launch multi-Grovlet real-process cluster; deploy N; prove cross-node Workflow -> Greeter; inspect state; kill Greeter node; detect/recover/reprove flow; stop/restart entire cluster from durable state; prove reconstruction; deploy N+1 and upgrade; exercise bad candidate/rollback; run resilience workflow; finish healthy; clean up all processes/artifacts.

## Constraints
No manual steps, shell orchestration, Docker requirement or fixed sleeps. All child processes are launched by the Go harness; failures dump useful diagnostics.

## Done
`go test ./...` passes. This marks the planned Grove MVP complete.