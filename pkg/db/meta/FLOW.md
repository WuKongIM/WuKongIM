# pkg/db/meta Flow

`pkg/db/meta` owns hash-slot-scoped metadata storage on shared
`pkg/db/internal` primitives. Storage code here must not import Pebble
directly.

## Core flow

1. `MetaDB` exposes stable `Shard` handles per physical hash slot.
2. Table specifications define rows and indexes; the registry drives
   `Tables()`, inspect scans, snapshots, and shared primary/index behavior.
3. Multi-hash-slot batches lock shards in sorted order, stage typed operations,
   commit once, and publish cache invalidations only after commit.
4. Channel-owned rows include Channel policy, subscribers, latest-message
   metadata, runtime routing metadata, and migration state.
5. UID-owned rows include users, devices, ordinary channel memberships, CMD
   channel memberships, plugin bindings, and message-event state.
6. Read-only inspect APIs expose stable bounded scans without mutating storage.
7. Hash-slot snapshot, backup, restore, and deletion operate on registered row,
   index, and system spans and clear affected caches after mutation.

## Membership-backed conversation directory

There is no registered conversation table. Table IDs 6 and 7 remain reserved
for the removed development-era ordinary and CMD conversation tables and must
not be reused.

`user_channel_membership` is UID-owned and keyed by:

```text
(uid, channel_id, channel_type)
```

It stores `join_seq`, monotonic badge `read_seq`, monotonic
`deleted_to_seq`, explicit `activated_at`, tombstone metadata,
`source_version`, and `updated_at`. Its directory index is:

```text
(uid, activated_at desc, channel_id, channel_type)
```

Point writes remove an obsolete activation-index key and install the new one in
the same batch. Directory pages scan one UID hash slot and return the complete
index cursor plus `done`; the limit bounds scanned rows. Ordinary message SEND
does not touch this table.

The membership reducer uses channel subscriber mutation `source_version` as a
stale cross-Slot write fence. Later live upserts preserve personal state,
rejoin after a later tombstone resets visibility from one captured Channel
tail, and same-version live state wins reset conflicts. Explicit read, hide,
and activation mutations reject tombstones and preserve `source_version`.

`user_cmd_channel_membership` is a separate UID-owned table keyed by:

```text
(uid, command_channel_id, channel_type)
```

It stores `start_seq`, monotonic `ack_seq`, tombstone metadata, and
`updated_at`. Bind/unbind and sync acknowledgement mutate this table; command
message SEND does not. CMD rows have no ordinary activation, read, or delete
fields.

## Other important tables

- Subscriber mutations sort and deduplicate UIDs and update the Channel's
  subscriber count and mutation version atomically with subscriber rows.
- Channel runtime metadata keeps routing, leadership, retention,
  `directory_ready`, terminal state, and write fences monotonic.
- Channel latest rows are channel-owned projections whose sequence only
  advances; they are not a per-user conversation directory.
- Message-event state, cursor, and applied-event tables preserve idempotent
  event reduction without raw replay rows.
- Migration tasks keep guarded runtime-meta updates and read-your-writes
  overlays inside the same deterministic batch.

Restore installs portable metadata into an isolated target, replays strictly
ordered Slot FSM commands, and uses canonical snapshot digests as replication
and final-verification fences.
