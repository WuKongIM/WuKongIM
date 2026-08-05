# internal/usecase/conversation Flow

## Responsibility

`internal/usecase/conversation` constructs transient ordinary conversation
responses from UID-owned `user_channel_membership` rows and Channel-owned
committed state. It also owns explicit badge, hide, and activation commands.
It does not persist conversation rows, subscribe users, perform online
delivery, or depend on access/cluster implementations.

## Directory list

```text
List(uid, opaque membership cursor, candidate limit, completed coverage)
  -> scan one UID-owned membership page ordered by
       (activated_at desc, channel_id asc, channel_type asc)
  -> emit tombstoned memberships as deletes without Channel reads
  -> hydrate all live candidates in one aligned batch
       (cluster groups the reads by exact Channel Leader)
  -> classify each candidate as conversation, omitted, delete, or unresolved
  -> return next_cursor, done, coverage, and tombstone retention metadata
```

The limit bounds membership candidates scanned, not conversations returned.
The server does not over-scan to fill a page, so an empty `conversations` array
does not imply completion; only `done=true` completes a directory pass.
Pagination is intentionally eventual and has no per-row revision. Clients
deduplicate channel keys, refresh the first page, and persist coverage only
after a complete pass.

For one live membership and hydrated head:

```text
visibility_floor = max(join_seq - 1, deleted_to_seq, retention_through_seq)
effective_read   = max(visibility_floor, read_seq, current_user_last_send_seq)
unread           = max(0, last_committed_seq - effective_read)
```

A channel is returned when a post-join/post-delete committed message exists or
when `activated_at > 0`. An inactive empty membership is omitted. An explicitly
activated empty channel is returned without a last message. A terminally
disbanded channel is returned as a delete, while a temporarily unavailable
Leader becomes unresolved and does not block cursor progress.

## Unresolved retry

```text
Retry(uid, channel keys)
  -> point-read current memberships
  -> convert missing/tombstoned rows to deletes
  -> batch-hydrate remaining rows by Channel Leader
  -> return aligned conversations, deletes, and still-unresolved keys
```

Retry is bounded to the directory page maximum and does not rewind directory
coverage.

## Personal state mutations

```text
ClearUnread
  -> verify one live membership
  -> hydrate the committed Channel head
  -> monotonically advance membership.read_seq to last_committed_seq

SetUnread(N)
  -> compute max(visibility_floor, last_committed_seq - N)
  -> monotonically advance membership.read_seq

DeleteConversation
  -> hydrate last_committed_seq
  -> monotonically advance membership.deleted_to_seq
  -> set membership.activated_at = 0

ActivateConversation
  -> record server time in membership.activated_at
```

`read_seq` is a badge floor, not a message read receipt or message-pull cursor.
SEND, receive, online delivery, and message pull never change it or
`activated_at`. The current user's latest committed ordinary SEND is computed
from the Channel sender-sequence index during hydration and is not written back
to membership.

## Cursor contract

The internal cursor carries the complete membership index position:

```text
(ActivatedAt, ChannelID, ChannelType)
```

The HTTP adapter encodes it opaquely. Last-message time and sequence do not
participate in directory ordering.
