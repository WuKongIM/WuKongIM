---
scope: package
summary: Exposes synchronous provider-neutral Cloud Lease commands while separating read-only inspection from paid mutation authority.
---

# Cloud Lease CLI Flow

## Responsibility

`cmd/wkcloudlease` is the command boundary for temporary provider-neutral
infrastructure lifecycle operations. It selects a provider adapter, constructs
the Cloud Lease controller, executes one synchronous operation, and emits
versioned non-secret output.
It does not own provider-neutral lifecycle policy, deployments, or workloads.

## Boundaries

- Lifecycle policy and reconciliation belong to `internal/usecase/cloudlease`;
  provider registration belongs in this composition package.
- Deployment and workload orchestration are separate consumers of Lease
  Receipts.
- Read-only quote/inspect authority never authorizes Acquire, Release, or Sweep.

## Main Flows

1. `dry-run` exercises the complete lifecycle using only the in-memory fake and
   cannot perform network or billable work.
2. Quote and Inspect strictly decode bounded versioned inputs and construct only
   read-authorized provider capabilities.
3. Acquire, access mutation, Release, and Sweep consume exact plans/selectors
   and construct only the explicitly mutation-authorized lifecycle adapter.

## Invariants and Failure Semantics

- Paid mutation independently requires the exact mutation-authorization
  environment value; inventory evidence has no authorization value.
- Commands reject unknown fields and identity mismatches and emit no secret
  provider material.
- Acquire and Release preserve partial/residual evidence on failure so cleanup
  remains possible.
- Every command is synchronous and starts no persistent worker.

## Read First

- [CLI entrypoint](main.go)
- [Lifecycle commands](lifecycle_commands.go)
- [Command tests](main_test.go)

## Update Triggers

Update this file when command authorization, provider construction, input
identity, paid lifecycle scope, or partial-cleanup evidence changes.
