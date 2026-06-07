# internalv2/usecase/conversation Flow

## Responsibility

`internalv2/usecase/conversation` owns entry-agnostic conversation list reads.
It does not depend on gateway frames, HTTP DTOs, clusterv2, or channel log
runtimes. Storage is supplied through small ports for UID-owned membership
pages and channel-owned latest projection batch reads.

## List Flow

```text
List(uid, cursor, limit)
  -> scan UID-owned user_channel_membership rows up to MaxMembershipScan
  -> deduplicate channel keys
  -> batch-read existing channel_latest rows
  -> skip memberships whose channel has no latest row
  -> sort by LastAt desc, LastMessageSeq desc, ChannelID asc, ChannelType asc
  -> apply the conversation cursor
  -> return limit rows plus the next cursor when another row remains
```

The usecase intentionally avoids per-member writes on message commit. A group
message advances one channel-owned latest projection asynchronously, while each
user's list is assembled only when that user asks for it.

`MaxMembershipScan` bounds CPU, memory, and storage reads per request. When the
scan stops early, `Truncated=true` tells the caller the page is complete only
inside the scanned membership window. This keeps the first implementation
predictable for very large channel sets without hiding the bounded-read tradeoff.

## Cursor Contract

The public cursor is based on the sorted conversation row, not the membership
storage cursor:

```text
(LastAt, LastMessageSeq, ChannelID, ChannelType)
```

This keeps pagination stable for the emitted sorted page. Membership storage
cursors remain an internal scan detail because the final order comes from
`channel_latest`, not from membership insertion order.
