---
scope: package
summary: Implements fail-closed Alibaba Cloud lease quoting, paid lifecycle, inventory, release, sweep, and OIDC bootstrap.
---

# Alibaba Cloud Lease Flow

## Responsibility

This package implements Alibaba Cloud adapters for read-only quotes, paid
lease acquisition, exact inventory, release and sweep, plus one-time OIDC
identity bootstrap.
It does not choose Lease policy, deploy WuKongIM, or run workloads.

## Boundaries

- Quote and inventory constructors are read-only. Paid lifecycle construction
  additionally requires the exact mutation authorization environment value.
- Automated jobs use temporary OIDC credentials and have no long-lived-key
  fallback.
- Provider mechanics live here; lease selection, approval, and run policy live
  in the cloud lease use case.

## Main Flows

1. Quote validates region capabilities, paginates completely, finds exact
   4-CPU/8-GiB capacity, checks quota and image facts, and returns the lowest
   complete postpaid estimate including the versioned EIP risk allowance.
2. Acquire creates one tagged VPC, vSwitch, security group, three service
   nodes, one load node, ESSD disks, and one EIP, then reconstructs an exact
   receipt from provider inventory.
3. Inspect, list, release, and sweep use complete tagged inventory; release
   deletes dependencies in bounded order and proves the lease empty.

## Invariants and Failure Semantics

- All compute is postpaid and non-spot; instances have no public address or
  NAT path, and only the load node receives the single EIP.
- Missing pages, repeated tokens, malformed prices, incomplete identity, or
  ambiguous child tags fail closed for quote/acquire and remain cleanup-only
  during release.
- Lifecycle mutation is enabled only when
  `WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION` exactly equals
  `create-and-delete-paid-cloud-lease`.
- Auto-release tags are a backstop, never proof of cleanup; release and sweep
  remain responsible for complete deletion.
- OIDC bootstrap creates only the documented roles and exact policies, and
  verifies them with a dry-run authorization probe.

## Read First

- [Quote adapter](openapi.go)
- [Lifecycle](lifecycle.go)
- [OpenAPI lifecycle](lifecycle_openapi.go)
- [Identity bootstrap](identity_bootstrap.go)
- [OpenAPI identity bootstrap](identity_bootstrap_openapi.go)

## Update Triggers

Update this file when topology, billing assumptions, mutation authorization,
tag identity, cleanup order, OIDC roles, or provider pagination changes.
