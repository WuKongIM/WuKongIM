---
scope: package
summary: Builds transient conversations from UID membership and Channel state, and owns badge, hide, and activation commands.
---

# Conversation Use Case Flow

## Responsibility

This package constructs ordinary conversation responses from UID-owned
membership rows and Channel-owned committed state. It owns explicit unread,
delete, and activation commands but persists no conversation rows.
It does not subscribe users, deliver messages, or implement storage and transport.

## Boundaries

- It does not subscribe users, deliver messages, or depend on access or cluster
  implementations.
- Directory pagination scans membership candidates, not a requested number of
  returned conversations, and hydration is one aligned Channel-Leader batch.
- `read_seq` is a badge floor, not a message read receipt or pull cursor.

## Main Flows

1. `List` scans one bounded UID membership page, emits tombstones as deletes,
   batch-hydrates live candidates, and returns conversations, unresolved keys,
   cursor, coverage, completion, and tombstone retention metadata.
2. `Retry` point-reads bounded keys, converts missing or tombstoned rows to
   deletes, batch-hydrates the rest, and does not rewind directory coverage.
3. Personal commands monotonically update `read_seq`, `deleted_to_seq`, or
   `activated_at` after exact membership and Channel-head reads.

## Invariants and Failure Semantics

- `visibility_floor = max(join_seq - 1, deleted_to_seq, retention_through_seq)`;
  unread is clamped from committed head against that floor, badge state, and
  the current user's latest committed send.
- Empty results do not imply completion; only `done=true` completes a pass.
- Disbanded channels become deletes. Temporary leader failure becomes
  unresolved and does not block cursor progress.
- Inactive empty membership is omitted; explicit activation returns an empty
  conversation without a last message.
- SEND, receive, delivery, and pull never mutate `read_seq` or `activated_at`.
- The opaque cursor contains `(ActivatedAt, ChannelID, ChannelType)` only.
- Hydrated payload bytes are cloned once into usecase-owned immutable data and
  may then be transferred through synchronous response adapters without another copy.

## Read First

- [Conversation application](app.go)
- [Unread calculation](unread.go)
- [Conversation contracts](types.go)
- [Membership pagination tests](membership_list_test.go)

## Update Triggers

Update this file when membership ordering, hydration, visibility or unread
math, unresolved retry, personal mutations, activation, or cursor shape changes.
