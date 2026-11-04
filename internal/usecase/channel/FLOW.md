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
4. Personal `AddAllowlist` writes the ACL, prepares each distinct canonical pair
   through the generation-fenced person directory, then activates only the
   whitelist owner. Existing join/read/delete boundaries are preserved.

## Invariants and Failure Semantics

- Ordinary subscribers create user-channel membership projections and observer
  events. Personal allowlist addition separately ensures both person Memberships
  through the person directory; it emits no ordinary subscriber observer event.
- Personal add requires both directory and activation capabilities before ACL
  mutation. Invalid pair identities fail before writes. Preparation/activation
  failures return errors after the durable prefix; retry repeats both steps.
  Group lists, denylist changes, replacement and removal keep their ACL-only
  semantics. Empty-conversation display is unchanged.
- Reset removes the old snapshot and adds the replacement under one mutation
  version.
- Observer notification occurs only after durable mutation, projection, and
  large-group refresh succeed.
- First allowlist or denylist add may create its derived channel; removal from
  a missing list is a zero-change no-op.

- Source-Channel send-ban operations validate canonical person identity and
  reject CMD-derived keys. Channel-info updates preserve an omitted send_ban at
  apply and atomically version explicit changes; existing-only Manager patches
  retain directory and membership state. Disband cannot be reversed. Explicit
  policy changes and strict creation emit bounded audit events with apply-time
  old/new state; omitted fields do not claim a policy mutation.

## Read First

- [Application service](app.go)
- [Use-case types](types.go)
- [Import boundary](import_boundary_test.go)

## Update Triggers

Update this file when channel commands, store extensions, member-list encoding,
versioning, membership projection, large-group policy, or observation changes.
