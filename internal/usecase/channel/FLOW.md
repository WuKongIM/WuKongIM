---
scope: package
summary: Orchestrates channel metadata, ordinary membership, derived member lists, versioning, projection, and bounded iteration.
---

# Channel Use Case Flow

## Responsibility

This package owns entry-independent channel metadata and membership policy,
including ordinary subscribers, temporary lists, allowlists, denylists,
mutation versions, and bounded page or chunk iteration.
It does not own entry protocols, concrete storage, cluster transport, or caches.

## Boundaries

- Storage ports represent cluster-authoritative Slot metadata; a single-node
  cluster uses the same path.
- Exact create, patch, membership, and non-empty operations fail closed without
  their narrow store capability and never scan a 100,000-member list as a
  fallback.
- HTTP, gateway, cluster, concrete storage, and runtime cache types remain
  outside the use case.

## Main Flows

1. Metadata commands create, patch, upsert, or delete through conditional
   exact-store operations while preserving fields outside the requested patch.
2. Ordinary subscriber mutation updates durable membership, projects the same
   logical version into the UID membership index, refreshes the large-group
   flag, then notifies the observer with cloned final state.
3. Allowlist, denylist, and temporary members use stable derived channel IDs
   that preserve the legacy internal namespace; counted mutations require the
   parent and return exact requested and durable set-change counts.

## Invariants and Failure Semantics

- Only ordinary subscribers create user-channel membership projection rows and
  observer events.
- Reset removes the old snapshot and adds the replacement under one mutation
  version.
- Observer notification occurs only after durable mutation, projection, and
  large-group refresh succeed.
- First allowlist or denylist add may create its derived channel; removal from
  a missing list is a zero-change no-op.

## Read First

- [Application service](app.go)
- [Use-case types](types.go)
- [Import boundary](import_boundary_test.go)

## Update Triggers

Update this file when channel commands, store extensions, member-list encoding,
versioning, membership projection, large-group policy, or observation changes.
