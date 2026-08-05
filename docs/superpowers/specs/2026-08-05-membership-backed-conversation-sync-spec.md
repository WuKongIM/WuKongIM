# Membership-Backed Conversation Sync Spec

Status: accepted design

This specification supersedes the persisted-conversation, unified conversation
projection, dense/sparse message fanout, and asynchronous conversation
projection decisions in the earlier conversation redesign documents. A
conversation remains a server-built response model, but it is no longer a
durable row or a message-time projection.

## Problem Statement

The current recent-conversation design updates UID-owned conversation state as
messages are committed. At high message QPS, this turns one channel message
into many database mutations. A 100,000-member group can cause member-level
conversation activity fanout, UID Slot proposals, cache pressure, retry work,
and database contention even though the durable message itself is stored only
once in the channel log.

The message SEND path should have work that is independent of subscriber count.
At the same time, a reconnecting client still needs to discover its channels in
useful order, obtain a server-built conversation view, calculate badge state,
respect join and deletion boundaries, pull offline messages, and synchronize
ordinary and CMD traffic without mixing their sequence semantics.

The project has not been released. The design therefore does not need old data,
old storage formats, old conversation APIs, or a dual-read/dual-write rollout.

## Solution

Remove the durable conversation table and the conversation-active projection
runtime. Use the existing UID-owned `user_channel_membership` row as the durable
ordinary conversation directory and per-user state. Store CMD discovery and
acknowledgement state in a separate `user_cmd_channel_membership` table.

Steady-state Message SEND appends only to the appropriate channel log, updates
one channel-local sender-sequence index in the same message-storage batch, and
performs online delivery. It does not write ordinary membership, CMD
membership, or conversation state for recipients. The one setup exception is
the first persistent person-channel SEND: it establishes both participant
memberships before append and marks the channel `directory_ready`; later SENDs
must observe that marker locally or through the authoritative Channel RPC and
must not repeat those writes.

The client synchronizes membership candidates in pages ordered by
`activated_at`. The server groups the page's channels by Channel Leader, reads
committed ordinary tails and the current user's latest committed sender
sequence in batches, and constructs transient conversation responses. The
client retains a separate per-channel message cursor for message delta pulls.
Active channels are discovered first; inactive channels are completed in the
background and are eventually consistent.

Ordinary messages and CMD/`SyncOnce` messages use separate logs and separate
membership state. This makes ordinary sequence subtraction meaningful for
badge calculation and prevents CMD traffic from changing ordinary conversation
tails.

## User Stories

1. As a message sender, I want SEND latency to be independent of channel member count, so that a 100,000-member group does not make one message disproportionately expensive.
2. As an online recipient, I want messages delivered in real time without waiting for conversation projection, so that conversation metadata work cannot delay delivery.
3. As a reconnecting user, I want my most explicitly active channels synchronized first, so that the useful part of my chat list becomes available quickly.
4. As a user with many inactive channels, I want the remaining directory synchronized in bounded background pages, so that one login does not create an unbounded database burst.
5. As a client developer, I want an opaque, stable directory cursor and an explicit `done` signal, so that an empty result page is not mistaken for completion.
6. As a client developer, I want the page limit to bound scanned memberships rather than promise a fixed number of conversations, so that server work remains predictable.
7. As a client developer, I want server-built conversation metadata, so that badge and visibility semantics are consistent across clients.
8. As a client developer, I want message delta cursors to remain client-local and separate from directory pagination, so that one failed channel does not discard directory progress.
9. As a user, I want pinning and final UI ordering to remain a client decision, so that the server's activation priority does not override local presentation choices.
10. As a user, I want opening or switching to a channel to make it an active synchronization candidate, so that explicitly used channels are returned earlier.
11. As a user, I do not want message receipt or message SEND to rewrite `activated_at`, so that high message QPS cannot move every recipient's directory index.
12. As a user, I want hiding a conversation to remove its currently visible history while retaining channel membership, so that a later message can make the conversation reappear.
13. As a removed member, I want a membership tombstone to remove the channel from my directory and prevent further ordinary message pulls.
14. As a rejoining member, I want visibility to restart at the new join boundary, so that messages from the previous membership period do not reappear.
15. As a newly added group member, I want all members in the same logical add operation to share one join boundary, so that member creation does not read the channel tail once per UID.
16. As a group administrator, I want repeated Add operations to be idempotent, so that retrying an existing member does not reset that member's badge, deletion, or activation state.
17. As a group administrator, I want a reset that retains a member to retain that member's personal state, so that replacing a subscriber snapshot does not look like leave and rejoin.
18. As a group administrator, I want a true remove followed by a later add to reset the member's visibility floor, so that rejoin semantics remain distinct from duplicate Add.
19. As a caller of member-management commands, I want a failed subscriber/membership double write reported as an error, so that I can retry the whole idempotent operation.
20. As an operator, I accept the bounded inconsistency window when a caller never completes such a retry, so that the first implementation does not require a durable projection task system.
21. As a user, I want `read_seq` changed only by explicit clear-unread or cap-unread commands, so that it represents badge state rather than per-message read receipts.
22. As a user, I want my own latest sent ordinary message to act as an effective badge floor, so that my own SEND does not create a badge for messages I implicitly saw before sending.
23. As a client developer, I want clear-unread to advance the badge floor to the latest committed ordinary sequence, so that the badge is removed deterministically.
24. As a client developer, I want set-unread to mean “cap unread at N,” so that `read_seq` remains monotonic and the API cannot move a cleared badge backward.
25. As a new member, I want message pull clamped to `join_seq`, so that I cannot request history from before I joined.
26. As a user who hid history, I want message pull clamped past `deleted_to_seq`, so that an old client cursor cannot restore hidden messages.
27. As a user, I want message pull clamped past the retention boundary, so that unavailable physical history is not counted or requested.
28. As a user, I want an activated empty channel to be representable as an empty conversation, so that explicitly opened channels can appear before their first visible message.
29. As a user, I do not want every newly joined empty channel to become a recent conversation, so that membership alone does not flood the chat list.
30. As a user, I want a hidden conversation with no newer message omitted, so that hiding has an observable effect.
31. As a user, I want the same hidden conversation to reappear after a newer ordinary message, so that hide is not equivalent to leaving the channel.
32. As a CMD consumer, I want CMD discovery and acknowledgement state isolated from ordinary conversation state, so that command traffic cannot alter ordinary badges or tails.
33. As a CMD consumer, I want CMD sends to append once and avoid recipient membership writes, so that CMD traffic has no message-time fanout either.
34. As a CMD sender, I accept that durable offline delivery to arbitrary per-message UIDs requires prior binding or a derivable membership set, so that the system does not hide recipient-index fanout behind another name.
35. As a person-chat participant, I want both UID-owned membership rows established before the first persistent message is appended, so that either participant can discover the chat after reconnecting.
36. As a person-chat sender, I want later messages to skip repeated membership checks after `directory_ready` becomes true, so that the normal SEND path remains constant-time.
37. As a user, I want one temporarily unavailable channel isolated as an unresolved item, so that it does not block all other conversations in the directory page.
38. As a client developer, I want unresolved channel keys retryable independently, so that directory coverage can advance without losing failed conversation hydration.
39. As an operator, I want channel disband to be one terminal channel mutation, so that disbanding a 100,000-member group does not synchronously fan out 100,000 membership tombstones.
40. As a user, I want disbanded channels rejected by conversation construction and message pull, so that stale membership rows cannot revive a terminal channel.
41. As an operator, I want disbanded channel identities never reused, so that stale membership rows cannot authorize a different future channel incarnation.
42. As an operator, I want low-cardinality evidence that warmed steady-state SEND performs zero recipient membership writes, so that future regressions can be detected.
43. As an operator, I want batch RPC counts, unresolved counts, page scan sizes, and local tail-read costs bounded and observable, so that sync-side read amplification can be managed.
44. As a developer, I want single-node cluster and multi-node cluster deployments to use the same routing and ownership semantics, so that topology does not create separate business paths.

## Implementation Decisions

### 1. Conversation is a transient server-built view

- There is no durable ordinary or CMD conversation table.
- There is no conversation-active runtime, authority cache, dirty-row queue,
  asynchronous conversation projector, or message-time conversation repair.
- The ordinary conversation use case remains as orchestration and response
  construction; removing persisted conversation state does not remove the
  domain term “conversation.”
- Earlier designs that retain conversation rows, distinguish dense and sparse
  message fanout, or asynchronously project recipient activity are superseded.

### 2. Ordinary membership schema and ownership

`user_channel_membership` is UID-owned and routes by `RouteKey(uid)`. A user's
directory page reads one UID Hash Slot rather than scanning all 256 Hash Slots.

The durable row contains:

```text
uid
channel_id
channel_type
join_seq
read_seq
deleted_to_seq
activated_at
tombstone
tombstone_at
source_version
updated_at
```

The stable primary key is:

```text
(uid, channel_id, channel_type)
```

The synchronization-priority index is:

```text
(uid, activated_at DESC, channel_id, channel_type)
```

`activated_at` is not part of the primary key. Moving an activation therefore
updates the row and its secondary index entry without changing point-lookup
identity.

There is no per-row directory `revision`. Directory pagination is an eventual,
non-snapshot scan. Clients deduplicate by `(channel_id, channel_type)`, refresh
the first page periodically, and eventually complete all pages.

### 3. Activation semantics

- New membership starts with `activated_at=0`.
- Explicit open, switch, or resume commands may set it from server time.
- Message SEND, message receipt, online delivery, and message pull do not
  update it.
- Repeated activation of the same channel may be coarsened or rate-limited
  because activation is synchronization priority rather than an exact event
  timestamp.
- Hiding a conversation sets `activated_at=0` immediately.
- The server uses the index order only to prioritize synchronization. The
  client owns pinning and final display order.

### 4. Badge state

`read_seq` is a badge baseline, not a message read receipt and not a message
sync cursor. It changes only for explicit clear-unread or cap-unread commands.

For the latest committed ordinary sequence `L`, clear-unread sets:

```text
read_seq = L
```

Cap-unread at `N` computes a target at or after the visibility floor and only
advances the stored value:

```text
target   = max(visibility_floor, L - N)
read_seq = max(stored_read_seq, target)
```

The subtraction is saturated at zero. `N=0` is equivalent to clear-unread. A
larger requested unread count never moves `read_seq` backward.

### 5. Hide, remove, and rejoin are different transitions

Hiding a conversation preserves valid membership:

```text
deleted_to_seq = max(deleted_to_seq, latest_committed_ordinary_seq)
activated_at   = 0
tombstone      = false
```

No visible ordinary message at a sequence at or below `deleted_to_seq` is
returned by conversation construction or message pull. A later message above
the boundary can make the conversation visible again.

Leaving or being removed writes a stable tombstone after subscriber removal:

```text
tombstone      = true
tombstone_at   = server_time
source_version = subscriber_mutation_version
```

The existing join, badge, delete, and activation fields are retained for
diagnosis and deterministic reducer behavior. A tombstone never triggers a
channel-log read during directory synchronization.

A true later rejoin resets the visibility state from a newly captured committed
channel tail `L2`:

```text
join_seq       = L2 + 1
read_seq       = L2
deleted_to_seq = L2
activated_at   = 0
tombstone      = false
```

A client that observes a later `join_seq` must discard cached history below the
new boundary even if it missed the intervening tombstone.

### 6. Subscriber and membership responsibilities

The channel-owned subscriber set remains responsible for online delivery and
send authorization. The UID-owned membership row is responsible for ordinary
conversation discovery, conversation construction eligibility, and ordinary
message pull eligibility.

Conversation construction and ordinary message pull do not perform a second
subscriber lookup. A valid, non-tombstoned membership is sufficient, subject
to the channel not being terminally disbanded.

Adding members is a synchronous, ordered double write:

```text
capture one committed channel tail L for the logical add
write channel-owned subscriber rows
write UID-owned membership rows with join_seq=L+1 and floors=L
return success only after both writes succeed
```

Removing members is also synchronous and ordered:

```text
remove channel-owned subscriber rows first
write UID-owned membership tombstones second
return success only after both writes succeed
```

Any failure is returned to the caller, which retries the whole idempotent
operation. The first version deliberately has no durable projection task or
background repair workflow. It accepts the inconsistency window if a caller
does not complete a required retry.

One logical bulk add reads the channel tail once and applies the same join
boundary to all members, including a 100,000-member add. UID writes remain
bounded and grouped by UID Hash Slot.

### 7. Source-version reducer

`source_version` is the channel's durable subscriber mutation version. It is a
cross-Slot stale-write fence, not a client pagination revision.

The membership reducer follows these rules:

```text
incoming source_version < stored source_version
    ignore the operation

row absent + live upsert
    initialize join/read/delete/activation from the captured tail

stored live + later live upsert
    preserve join/read/delete/activation; advance source_version only

stored tombstone + later-version live upsert
    perform a true rejoin and reset join/read/delete/activation

stored tombstone + same-version live upsert
    clear tombstone but preserve personal state

same source_version conflict
    live upsert wins over tombstone
```

The same-version live-wins rule preserves members retained by a reset operation
that removes an old snapshot and adds the replacement under one logical source
version. Explicit badge, hide, and activation commands preserve
`source_version` and reject or no-op when the row is tombstoned.

### 8. Person-channel directory readiness

Persistent person channels use their canonical person-channel identity. On the
first persistent SEND, the system captures the current committed tail, ensures
both participants have UID-owned membership rows initialized from that tail,
then advances a person-only channel metadata flag:

```text
directory_ready: false -> true
```

Only after both membership rows exist and `directory_ready` is durable may the
first persistent message be appended. The flag is monotonic and is not cleared
by hide, badge changes, block state, or conversation deletion. Later SENDs read
cached or authoritative channel metadata and append without rechecking both UID
rows. The authoritative Channel RPC must carry `directory_ready`; omitting it
turns a remote read into repeated membership proposals.

Person membership is not removed by the user hiding or deleting a local
conversation. Person-channel authorization derives from the canonical pair,
not the ordinary group subscriber table.

### 9. Ordinary and CMD isolation

Ordinary durable messages use the ordinary channel log. CMD and `SyncOnce`
messages use CMD channel logs. A CMD record must not consume ordinary channel
sequence space or become an ordinary last message.

This isolation is required for ordinary badge arithmetic. If CMD records share
ordinary sequence space, `last_seq - effective_read_seq` is not an ordinary
message count.

CMD discovery state is stored separately:

```text
user_cmd_channel_membership

uid
command_channel_id
channel_type
start_seq
ack_seq
tombstone
tombstone_at
updated_at
```

The stable primary key is `(uid, command_channel_id, channel_type)`. CMD rows do
not have `activated_at`, ordinary `read_seq`, ordinary `deleted_to_seq`, or a
directory revision. Bind and unbind mutate this table; CMD message SEND does
not. Message sync acknowledgement advances `ack_seq` in batches rather than at
SEND time.

Durable offline discovery for arbitrary per-message UIDs requires those UIDs
to be bound before SEND or to be derivable from existing membership. Otherwise
the per-message target is online-only; the design does not add recipient-index
writes on each CMD message.

### 10. Committed sender-sequence index

The server calculates the ordinary badge using the current user's latest
committed ordinary SEND. It must not find that sequence by unbounded reverse
scanning of the channel log.

Message storage maintains one secondary index entry per ordinary durable
message:

```text
channel_sender_seq_index

key:   (channel_key, from_uid, message_seq)
value: message_id
```

The entry is written atomically with the message row. CMD and `SyncOnce`
messages do not write this ordinary index.

Conversation construction reverse-seeks the current UID prefix and accepts the
first entry whose sequence is at or below committed HW. This is intentionally
an ordered per-message index rather than one mutable sender-tail row: a mutable
tail could point at a durable but uncommitted record and could not recover the
previous committed sender sequence after truncate.

Truncate and retention remove index entries atomically with their message rows.
Follower apply, backup, restore, and cleanup treat the index as a deterministic
message-storage projection.

### 11. Server-built conversation calculation

For one valid ordinary membership, define:

```text
visibility_floor = max(
    join_seq - 1,
    deleted_to_seq,
    retention_through_seq,
)

effective_read_seq = max(
    visibility_floor,
    read_seq,
    current_user_last_committed_send_seq,
)

unread = max(0, last_committed_ordinary_seq - effective_read_seq)
```

The current user's latest SEND acts as evidence that the user has seen all
ordinary messages through that sequence. This avoids a membership write on
SEND while preventing the user's own message from creating a badge.

The server emits a conversation when either condition is true:

```text
last_committed_ordinary_seq >= join_seq
and last_committed_ordinary_seq > deleted_to_seq

or

activated_at > 0
```

A newly joined, inactive channel with no post-join message is omitted. An
explicitly activated empty channel is emitted with no last message and zero
unread. A hidden channel is omitted until a message exceeds its delete floor or
the user explicitly activates it again.

### 12. Directory pagination

The directory query scans membership candidates in:

```text
activated_at DESC, channel_id ASC, channel_type ASC
```

The external cursor is opaque but encodes the full index position. The page
limit bounds membership candidates scanned, not conversations returned. The
server never over-scans in an attempt to fill a response with a requested
number of conversations.

Consequently, an empty conversation array does not mean completion. Only
`done=true` completes a pass. The cursor always advances to the last scanned
membership candidate.

The scan is not snapshot-consistent. Activation changes can create duplicates
or move rows ahead of a cursor. Clients deduplicate by channel key, periodically
restart from the first page, and perform low-priority complete passes. No
message-time membership mutation is introduced to improve this eventual
ordering.

The client persists directory coverage only after reaching `done=true`.
Directory responses carry coverage and tombstone-retention information. A
client whose last completed coverage predates retained tombstones receives
`reset_required` and rebuilds local directory state instead of assuming that
expired deletions were observed.

### 13. Batch conversation hydration and partial results

For each membership page, the server resolves channel routes, groups requests
by exact Channel Leader, and sends one bounded batch per participating node.
The Channel Leader performs bounded local reads of committed channel tail,
sender-sequence index, retention state, last-message display fields, and
terminal channel state.

The response has separate collections for:

```text
conversations
deletes
unresolved
next_cursor
done
```

Per-candidate outcomes are:

```text
ok
no_visible_message
delete
retryable
```

Tombstones produce `delete` without a channel read. A channel that has no
visible message is a normal result and is not unresolved. A temporarily
unavailable channel becomes `retryable`; all other successful candidates are
returned and the directory cursor still advances.

Clients persist unresolved channel keys and retry them through a bounded batch
hydration request. The retry remains server-built: the client does not compute
the authoritative badge. A later complete directory scan is also allowed to
rediscover unresolved keys.

Batch interfaces preserve input/result alignment and hide metadata routing,
Leader grouping, bounded concurrency, retry normalization, and local storage
implementation details from the conversation use case.

### 14. Message pull

The client stores a separate local message cursor for every channel. Directory
cursor progress and message cursor progress are independent.

Ordinary message pull queries the server-side membership row. A missing or
tombstoned row is rejected. It does not perform a subscriber lookup. The server
clamps the requested start sequence:

```text
allowed_from_seq = max(
    requested_from_seq,
    join_seq,
    deleted_to_seq + 1,
    retention_through_seq + 1,
)
```

The client cannot bypass join, hide, or retention floors by submitting an older
cursor. Batch message pulls validate the same UID-owned membership set first,
then group channel reads by Channel Leader.

Online delivery updates the client's local message state directly. The client
does not continuously poll all memberships while connected. Reconnect performs
active directory pages first and completes inactive pages in the background.

### 15. Read-amplification tradeoff

Removing message-time recipient fanout necessarily shifts change discovery to
reconnect-time reads. Without a per-user changed-channel inbox, the server
cannot know which of a user's inactive channels changed while the user was
offline without checking those channels.

The accepted consistency model is:

```text
online delivery: real time
active channel recovery: prioritized and near real time
inactive channel recovery: eventual, after background pagination
```

Each directory page uses bounded, node-grouped tail checks. A node-local,
non-durable cache may accelerate committed channel heads, but durable channel
log and sender-sequence indexes remain the correctness fallback. The cache does
not add database writes or become an authority.

### 16. Terminal channel disband

Disband is a terminal channel metadata state. It is one channel-owned mutation,
not a synchronous fanout of membership tombstones.

Once disbanded:

- message append and online delivery stop;
- conversation hydration returns `delete`;
- ordinary and CMD message pull reject the channel;
- stale UID membership rows do not revive the channel.

Membership cleanup may be bounded and deferred because it is not part of
correctness. The pair `(channel_id, channel_type)` is never reused after
disband. Supporting identity reuse would require an explicit channel
incarnation in membership and is outside this design.

### 17. Send-path and synchronization invariants

An ordinary SEND performs only:

```text
channel message-log append
same-batch channel_sender_seq_index write
online delivery
```

It performs zero ordinary membership writes, zero CMD membership writes, zero
conversation writes, and zero recipient UID Slot proposals. Its durable write
cost is `O(1)` with respect to channel member count.

For a page of `K` memberships, synchronization performs one UID Slot range
scan, a number of remote batch calls proportional to participating nodes rather
than `K`, and at most `K` bounded local channel-head/index reads.

Queues, batch rows, batch bytes, local concurrency, remote concurrency, retry
state, and cache size are bounded. Observability never labels metrics with UID,
channel ID, or other high-cardinality identities.

### 18. Greenfield replacement

The project is unreleased. Implementation directly replaces the development
storage and behavior:

- no old data migration;
- no old row decoding;
- no dual reads or dual writes;
- no shadow comparison rollout;
- no compatibility adapter for the old conversation projection;
- no sender-index backfill.

Development databases may be recreated. `directory_ready` remains a live
person-channel invariant and is not a migration readiness flag.

## Testing Decisions

The primary acceptance seam is process-level black-box behavior through public
member-management, SEND, conversation-directory sync, message-pull, CMD-sync,
badge, hide, activation, and disband entries. Tests exercise both a single-node
cluster and a multi-node cluster. They assert user-visible results and cluster
semantics rather than internal function calls.

Narrower tests are used only where the black-box seam cannot efficiently or
deterministically observe a required invariant:

1. Message-storage integration tests cover atomic sender-sequence index writes, reverse seek at or below committed HW, follower apply, truncate, retention, backup, restore, and index cleanup.
2. Membership-storage integration tests cover stable primary lookup, activation-index pagination, old-index-key removal, tombstone retention, and field encoding.
3. Membership reducer tests cover stale source versions, duplicate Add, true rejoin, same-version reset live-wins behavior, and tombstoned user-state commands.
4. Cluster batch tests cover route resolution, grouping by Channel Leader, ordered result alignment, bounded concurrency, one failing Leader group, and unresolved normalization.
5. Conversation use-case tests cover badge equations, own-SEND floor, hide/reappear, empty activated conversation, omitted empty inactive membership, tombstone delete, page underfill, empty nonterminal pages, and opaque cursor continuation.
6. Message-pull tests cover membership-only eligibility and clamping to join, delete, and retention floors without subscriber lookup.
7. CMD tests prove log, sequence, directory, acknowledgement, and tombstone isolation from ordinary conversations.
8. Person-channel tests prove that both membership rows exist before the first persistent message and that later SENDs use `directory_ready` without repeated UID fanout.
9. App wiring tests prove that post-directory message commit has no conversation projector or recipient membership mutation dependency.
10. Performance/integration tests send to a 100,000-member group and assert that durable membership/conversation mutations remain zero and SEND work does not scale with member count.
11. Multi-node sync tests use a page whose channels span several Leaders and assert that remote calls are grouped by node rather than issued per channel.
12. Failure tests prove that one unavailable channel produces one unresolved item while successful conversations, deletes, cursor progress, and `done` semantics remain valid.
13. Disband tests prove one channel-terminal mutation blocks later append, hydration, and pull without synchronous membership fanout.

Default unit tests remain fast. Real-process, realistic-duration, large-group,
and multi-node timing scenarios use the repository's integration or E2E build
tags. Performance assertions rely on operation counts, bounded queue/RPC
evidence, and benchmark measurements rather than fragile wall-clock thresholds
alone.

The relevant package and architecture flow documents are updated in the same
change whenever the implementation makes their current conversation projection
descriptions inaccurate.

## Out of Scope

- Preserving development-only conversation rows, APIs, configuration, metrics, or on-disk formats.
- Migrating or backfilling existing conversation, CMD conversation, membership, or message index data.
- A durable subscriber-to-membership projection task, outbox, or automatic repair worker.
- Snapshot-consistent directory pagination or a per-row client revision stream.
- A per-user changed-channel inbox or any message-time recipient directory fanout.
- Updating `read_seq` for each viewed, delivered, pulled, or sent message.
- Server-controlled pinning or final client UI ordering.
- Durable offline CMD discovery for arbitrary unbound per-message UIDs.
- Reusing a disbanded channel identity or adding channel-incarnation semantics.
- Treating a single-node deployment as a non-cluster path.
- Exact immediate discovery of every inactive changed channel after reconnect.

## Further Notes

- The design intentionally prefers bounded SEND cost over immediate inactive
  conversation discovery. This is the central tradeoff, not an implementation
  accident.
- `source_version` and a client directory `revision` solve different problems.
  The former is required to reject stale cross-Slot membership projection
  writes; the latter is deliberately absent.
- `activated_at` is a synchronization-priority signal. It is neither last
  message time nor an instruction for final client sorting.
- `read_seq` is a monotonic badge floor. It is neither a read receipt nor the
  client's message synchronization cursor.
- The ordinary/CMD log split is a correctness requirement for sequence-based
  badge arithmetic, not merely an organizational preference.
- Conversation construction may continue to be named “conversation” in the
  use-case layer even though no durable conversation entity exists.
