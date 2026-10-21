---
scope: package
summary: Owns provider-neutral temporary cloud Lease validation, quote, acquisition, access grants, release, and sweep policy.
---

# Cloud Lease Control Flow

## Responsibility

This package owns provider-neutral temporary infrastructure lifecycle policy.
It contains no WuKongIM deployment, Slot, Channel, worker, or workload logic.

## Boundaries

- Providers supply quote, inventory, mutation, and zero-inventory proof through
  narrow ports; provider tags and inventory are authoritative.
- Receipts are non-secret. Bootstrap private keys never enter plans, tags, or
  receipts, and public keys are represented only by a normalized set digest.
- Consumers can validate receipts without receiving quote or mutation powers.

## Main Flows

1. Validate strict Plan v1, immutable digest and mandatory tags; quote capacity,
   quota, and price within remaining aggregate stop and hard limit.
2. Inspect exact Lease identity before Acquire; recover matching inventory after
   ambiguity, reject a different plan, and return partial inventory as cleanup-required.
3. Grant or revoke typed expiring access, and release or sweep only by exact
   selector until complete provider inventory yields zero-inventory proof.

## Invariants and Failure Semantics

- Quote never mutates. At most eight exact zone/compute exclusions are allowed;
  a public-egress ceiling requires at least one public IPv4 host.
- Idempotency binds repository, request, Lease, and Plan digest. Every resource
  repeats complete Lease tags and its logical role.
- Blank or lost workflow state never substitutes for provider inspection.
- Repeated exact grants, absent revocations, and post-deletion release are
  idempotent.
- Residual resources return `release_pending` and `ErrResidualResources`; the
  same exact Lease is retried rather than assumed deleted.
- A cleanup selector is derivable from admitted Plan and Quote before paid
  dispatch.
- Fake-provider output is contract-test evidence only; it cannot prove real
  capacity, price, permission, or zero inventory.

## Read First

- [Lifecycle controller](control.go)
- [Lease contracts](types.go)

## Update Triggers

Update this file when plan identity, cost admission, bootstrap access, receipt
validation, access windows, release proof, inventory authority, or sweep changes.
