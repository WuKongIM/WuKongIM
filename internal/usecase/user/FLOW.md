---
scope: package
summary: Orchestrates legacy-compatible user tokens, device quit, online status, system UIDs, and restore cache reload.
---

# User Use Case Flow

## Responsibility

This package owns entry-independent user token persistence and verification,
device quit, online-status, system UID, and restore-time cache reload policy.
It does not own HTTP, gateway frames, concrete storage, or cluster transport.

## Boundaries

- Durable metadata and authority presence are injected ports supplied by app;
  HTTP, gateway frames, and concrete cluster types remain outside the package.
- A single node is still a single-node cluster and uses the same ports.
- The restore-only system UID read port is used only by full cache reload;
  foreground operations remain behind ordinary store fences.

## Main Flows

1. Token update validates identity and device fields, creates missing UID
   metadata, upserts per-device token state, and schedules owner-local
   same-device close for master-device replacement. CONNECT verification reads
   the same UID/device row, compares the opaque token, and returns its durable
   device level only after a match.
2. Device quit clears the selected stored token and schedules owner-local
   matching-device close; online status prefers authority routes when configured.
3. System UID commands persist the reserved set and maintain the process-local
   fast-check cache; restore resume replaces that cache from the restore port
   before foreground admission.

## Invariants and Failure Semantics

- Session close actions are owner-local effects triggered after the relevant
  durable token mutation.
- Missing, cleared, or mismatched device tokens fail CONNECT verification
  closed without exposing the stored credential.
- Online results contain one legacy item per active authority route.
- Cache reload replaces the complete set rather than incrementally merging
  restored and pre-restore state.
- The configured primary system UID is always recognized in addition to the
  durable system-UID registry; every cluster node must use the same value.
- Ordinary operations never bypass foreground storage fencing.

## Read First

- [User application](app.go)
- [User contracts](types.go)
- [Behavior tests](user_test.go)

## Update Triggers

Update this file when token or device semantics, close scheduling, online
status, system UID persistence, cache ownership, or restore reload changes.
