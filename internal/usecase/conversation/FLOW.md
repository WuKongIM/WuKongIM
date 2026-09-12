---
scope: package
summary: Builds transient conversations from UID membership and Channel state, and owns badge, hide, and activation commands.
---

# Conversation Use Case Flow

## Responsibility

This package constructs ordinary conversation responses from UID-owned
membership rows and Channel-owned state. Canonical previews read persisted
state; personal mutations and legacy sync read committed state. It owns explicit unread,
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
   reads persisted previews for live candidates, and returns conversations,
   cursor, coverage, completion, and tombstone retention metadata.
2. List admission is shared by all callers: at most 16 active pages, no waiting
   queue, and a five-second deadline. Any item failure fails the entire page;
   clients retain their original cursor and retry the same request.
3. `SyncLegacy` walks at most 1,000 membership candidates, applies the v2.2
   page, unread, excluded-type, version, and per-Channel cursor semantics, then
   reads recent committed messages and their stream-event summaries in aligned
   batches of at most 200 Channels. An old empty-client-number head can be
   reread after an advanced legacy cursor to repair a missing preview; ordinary
   heads retain exclusive-cursor behavior and durable read/delete state is unchanged.
4. Personal commands monotonically update `read_seq`, `deleted_to_seq`, or
   `activated_at` after exact membership and Channel-head reads.

## Invariants and Failure Semantics

- `visibility_floor = max(join_seq - 1, deleted_to_seq, retention_through_seq)`;
  unread counts ordinary messages after that floor, badge state, and the current
  user's latest send within the selected persisted or committed read boundary. SyncOnce/recovery positions are excluded by the
  Channel leader's rank query. SetUnread uses a leader-selected ordinary-message
  boundary; legacy pulls retain the actual effective read sequence, never infer
  it by subtracting an unread count from a sparse log sequence.
- Empty results do not imply completion; only `done=true` completes a pass.
- Disbanded channels become deletes. Temporary leader or storage failure fails the entire list page.
- The canonical `List` reads current-Leader disk LEO without runtime activation
  or quorum confirmation. Persisted messages may appear before SEND success.
  Legacy sync and personal mutations retain committed reads;
  `SyncLegacy` retries unresolved committed reads once and fails the whole request if any stay
  unresolved; it must not turn a temporary failure into silent conversation
  loss.
- A legacy client cursor overrides excluded-type and unread filtering for that
  Channel. Without such a cursor, positive `version` and `only_unread` reads
  start after the effective badge floor. Recent messages are returned newest
  first in the old raw-array envelope.
- Inactive empty membership is omitted; explicit activation returns an empty
  conversation without a last message.
- Unread commands are idempotent no-ops for missing/tombstoned memberships or
  a confirmed deleted Channel; they never create membership. Failed metadata or
  head reads remain errors, and delete-command absence semantics stay unchanged.
- SEND, receive, delivery, and pull never mutate `read_seq` or `activated_at`.
- The opaque cursor contains `(ActivatedAt, ChannelID, ChannelType)` only.
- Hydrated payload bytes are cloned once into usecase-owned immutable data and
  may then be transferred through synchronous response adapters without another copy.

- Imported list-only hidden memberships remain accessible to history reads.
  They appear after a newer ordinary message or explicit activation, without
  modifying read/delete floors or native unread calculations.

## Read First

- [Conversation application](app.go)
- [Unread calculation](unread.go)
- [Conversation contracts](types.go)
- [Membership pagination tests](membership_list_test.go)
- [Legacy sync compatibility](legacy_sync.go)

## Update Triggers

Update this file when membership ordering, hydration, visibility or unread
math, whole-page failure, personal mutations, activation, or cursor shape changes.
