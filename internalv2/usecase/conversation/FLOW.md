# internalv2/usecase/conversation Flow

## Responsibility

`internalv2/usecase/conversation` owns entry-agnostic conversation list reads
and committed-message conversation projection policy. It does not depend on
gateway frames, HTTP DTOs, clusterv2, or channel log runtimes. Storage is
supplied through small ports for UID-owned conversation active pages,
channel-owned current-page last-message reads, member classification, and
UID-owned conversation state batch writes.

## List Flow

```text
List(uid, cursor, limit)
  -> scan UID-owned conversation active index using (active_at, channel_id, channel_type) cursor
  -> keep the returned page order exactly as storage emits it
  -> build current-page last-message requests with visible_after_seq = deleted_to_seq
  -> batch-read newest visible messages from each channel-owned message log
  -> keep conversation rows whose channel has no visible last message
  -> calculate unread as max(last_message_seq - max(read_seq, deleted_to_seq), 0)
  -> clone payloads before returning
```

The usecase no longer scans `user_channel_membership` or reads
`channel_latest` for the active list path. `conversation.active_at` is the
authoritative ordering anchor; last-message time is display data only and does
not reorder sparse or dense rows. `SparseActive=true` rows therefore stay in
the active page position chosen by their UID-owned row even when the channel log
contains a newer message.

Rows without a visible last message are returned with `LastMessage=nil` and
`Unread=0`. List reads do not delete, hide, or repair conversation rows.

## Projector Flow

```text
HandleCommitted(event)
  -> if person channel:
       decode canonical person channel id
       upsert sender and peer dense conversation rows
  -> else:
       ask MemberSource to classify the channel with limit small_group_fanout_limit + 1
       if small:
         upsert one dense row per returned member
       else:
         upsert only the sender sparse row
```

Dense rows use `SparseActive=false`; sparse sender rows use
`SparseActive=true`. `ActiveAt` is always the committed event's
`ServerTimestampMS`, so projector retries do not reorder conversations based on
retry time. When a member has `JoinSeq`, the projector initializes both
`ReadSeq` and `DeletedToSeq` to `JoinSeq - 1` so later list reads do not expose
messages from before the user joined. Group projection requires a member
classifier and a positive small-group fanout limit; missing wiring returns a
configuration error instead of silently degrading small groups to sparse rows.

## Cursor Contract

The cursor is based on the active index row emitted by storage:

```text
(ActiveAt, ChannelID, ChannelType)
```

This matches the UID-owned active index order:

```text
active_at desc
channel_id asc
channel_type asc
```

Last message sequence is intentionally absent from the cursor because message
log tails do not participate in pagination order.
