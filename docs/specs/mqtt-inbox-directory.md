# MQTT inbox directory discovery

## Failure inventory (before implementation)

- Activation, read/delete floors, hidden conversations and tombstones must not
  remove an existing directory key from initial inbox discovery or move its cursor.
- Paging must follow the existing primary key's byte-length/string/type order,
  including the full type tie-breaker, and remain isolated to the requested UID.
- A page has at most 64 entries and inspects at most limit+1 primary witnesses.
  Non-person entries consume this budget; filtering them cannot cause an unbounded
  search for the next person channel. Malformed keys/values fail the whole page.
- Each page must use one snapshot after a fresh current UID Slot authority/apply
  barrier. Live mutations cannot replace that page's pinned evidence. Cancellation,
  changed routing or unavailable authority cannot turn into authoritative absence.
- Requests reject irrelevant fields and invalid bounds/cursors. Replies reject
  mixed result types, duplicates, regressions, cursor mismatches and short nonfinal
  pages. Existing read kinds reject the new result field.
- Older requests/replies must retain their JSON shape; old peers reject the new
  kind. This is a matched-binary addition, not a new table or RPC service.

## Durable cursor bounds

The initial-projection checkpoint must accept every legal directory candidate,
including 1025–4096-byte IDs belonging to other channel types or tombstones.
Failure inventory: scan success must not be followed by an unpersistable cursor;
UTF-8 bounds are bytes, not characters; values over 4096 bytes and invalid
identities must fail; checkpoint ordering cannot regress; backup verification
and restore must preserve the exact ID/type and permit resumption after it.

The UID binding reuses directory-key validation for its existing discovery
columns. No table, column, envelope or index changes. Long checkpoints require
matched readers/writers/tools because older validation rejected IDs over 1024
bytes. Preserve a pre-feature backup for rollback.

## Contract

Read kind 20, `MQTTReadInboxDirectory`, uses an `MQTTBindingUID` Owner and the
existing RPC 91 authority path. It returns ordinary `user_channel_membership`
primary keys through `Directory`, with a complete `After.Directory` ChannelKey.
All channel types and tombstones remain candidates. It does not return or apply
personal visibility state, source identity, authorization or retention permission.

The scan order is `(byte length of ChannelID, ChannelID bytes, ChannelType)`.
Limits are 1–64, excluding one validated lookahead. Empty final pages preserve
the input cursor; all nonempty pages return their last emitted key. Each page is
pinned independently; this does not constitute a snapshot spanning many pages.

## Required admission handshake

Inbox preparation must commit its UID qualification before starting this scan.
A newly created person channel must commit both UID directory registrations,
then read current UID qualifications, and establish required source protection
before its first persistent append. Preparing qualifications already participate.
Together these orderings cover insertions behind a scan cursor. The existing
asynchronous person-directory task alone does not meet this pre-append contract.

InboxEstablishment now uses this read after committing UID qualification;
InboxAppender drives future-source admission behind the private composition gate.
Offline maintenance, safe cleanup and complete product listener integration remain
required. Completing a directory page authorizes
neither SUBACK nor delivery nor source reclamation.
