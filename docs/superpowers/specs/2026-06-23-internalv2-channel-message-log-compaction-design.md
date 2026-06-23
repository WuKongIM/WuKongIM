# Internalv2 Channel Message Log Compaction Design

## Context

`internalv2` needs a manager operation that deletes channel messages before or through a selected `messageSeq`. The old `internal` path already models this as channel retention: advancing a cluster-authoritative `RetentionThroughSeq` instead of deleting Pebble rows directly.

For `internalv2` / `channelv2`, the same feature must be treated as replicated log compaction, not as ordinary message deletion. A channel message log is similar to a Raft log: every replica must agree on the compacted prefix, later leaders must inherit that boundary, and reads must never expose entries below the committed compaction boundary even if local physical deletion is delayed.

The first implementation should intentionally stay small: add the authoritative logical compaction boundary and make read paths respect it. Physical trim, snapshot/reset install, and automatic TTL workers are later phases.

## Goals

- Support deleting a channel message prefix through an inclusive `message_seq` boundary.
- Reuse the existing authoritative metadata field `RetentionThroughSeq` as the channel message log compaction boundary.
- Keep the boundary monotonic and replicated through slot metadata so leader changes cannot make compacted messages visible again.
- Make manager reads, message sync reads, latest-message reads, and direct message lookup paths hide `message_seq <= RetentionThroughSeq`.
- Keep the manager usecase entry independent from cluster, RPC, Pebble, and gateway details.
- Preserve high-scale behavior for large channels: no full channel scans on request paths, no per-message tombstones, and no sequence holes.

## Non-Goals

- Do not physically delete message rows in the first `internalv2` implementation.
- Do not delete arbitrary messages in the middle of a channel log.
- Do not add a separate single-node path; a single node is still a single-node cluster.
- Do not implement timestamp-based deletion in the first version. Timestamp deletion can later be converted by the leader to a contiguous prefix boundary.
- Do not implement channel snapshot install, retention reset, or follower catch-up below the compaction floor in the first version.
- Do not add a TTL worker in the first version.
- Do not change configuration in this feature slice.

## Public Semantics

Manager deletion should expose an inclusive boundary:

```text
through_seq = N
messages with message_seq <= N are no longer visible
min_available_seq = N + 1
```

If a future API wants the wording "delete before messageSeq", the access layer should translate it to this inclusive model:

```text
delete before message_seq X => through_seq = X - 1
```

The first version should use the existing manager shape:

```http
POST /manager/messages/retention
```

```json
{
  "channel_id": "room-1",
  "channel_type": 2,
  "through_seq": 1024,
  "dry_run": false
}
```

Response status should distinguish successful advance, advisory dry-run, no-op, and safety blocking:

```json
{
  "channel_id": "room-1",
  "channel_type": 2,
  "requested_through_seq": 1024,
  "advanced_through_seq": 1000,
  "min_available_seq": 1001,
  "status": "advanced",
  "blocked_reason": ""
}
```

Advancing to a lower safe boundary than requested is valid when safety gates lag. If no safe progress is possible, return `status: "blocked"` with a precise reason.

## Core Watermarks

| Watermark | Owner | Meaning |
| --- | --- | --- |
| `RetentionThroughSeq` | Slot metadata / channel runtime meta | Authoritative compacted prefix. Entries `<= RetentionThroughSeq` are unavailable for reads. |
| `MinAvailableSeq` | Derived runtime value | First readable message sequence, `max(RetentionThroughSeq + 1, 1)`. |
| `HW` | Channel runtime | Highest quorum-committed message sequence. |
| `CheckpointHW` | Channel runtime / durable checkpoint | Highest committed sequence durably checkpointed for recovery. |
| `MinISRMatchOffset` | Channel leader runtime | Minimum observed match offset among current ISR members in the current leader epoch. |
| `CommittedReplayCursor` | Channel store / replay runtime | Durable progress for committed side-effect replay. |
| `LEO` | Channel runtime / store | End of the local message log. Logical compaction must not make it regress. |

`RetentionThroughSeq` is named as retention in existing metadata, but for this feature it should be documented and used as the channel log compaction boundary.

## Required Invariants

1. `RetentionThroughSeq` is monotonic for a channel.
2. Only the current channel leader can compute and advance the boundary.
3. The advance must be fenced by channel epoch, leader epoch, leader ID, and lease information from the latest runtime view.
4. `RetentionThroughSeq <= HW`.
5. `RetentionThroughSeq <= CheckpointHW`.
6. `RetentionThroughSeq <= MinISRMatchOffset` for the current ISR, so current replicas have the compacted prefix before it becomes unavailable.
7. `RetentionThroughSeq <= durable CommittedReplayCursor`, so delivery/conversation committed side effects are not skipped.
8. Every read path clamps or filters to `message_seq >= MinAvailableSeq`.
9. Logical compaction never lowers `LEO`; new appends continue from the existing end of log.
10. A future physical trim may lag logical compaction, but lag must never make compacted messages visible.
11. A future follower whose local log is below `RetentionThroughSeq` needs an explicit retention reset or snapshot catch-up before joining ISR or becoming leader.

These invariants are the main reason this feature belongs beside channel/slot metadata semantics rather than in a manager storage-delete handler.

## Architecture

```text
manager HTTP
  -> internalv2/usecase/management
  -> internalv2/app message compaction operator
  -> local channel leader or node RPC to channel leader
  -> channelv2 runtime compaction view
  -> slot metadata AdvanceChannelRetentionThroughSeq command
  -> channelv2 read-floor application
```

Layer responsibilities:

- `internalv2/access/manager`: HTTP DTO validation and error mapping only.
- `internalv2/usecase/management`: request validation and stable usecase contract only.
- `internalv2/app`: leader routing, safety gate calculation, metadata advance, local runtime application, and RPC delegation.
- `internalv2/access/node`: leader RPC codec and handler for remote manager requests.
- `internalv2/infra/cluster`: adapters from usecase/app ports to `pkg/clusterv2` and manager message readers.
- `pkg/clusterv2`: public node methods that expose channel runtime compaction views and propose the metadata advance through existing slot FSM commands.
- `pkg/channelv2`: runtime/store read floor semantics and projection of `RetentionThroughSeq` from authoritative metadata.

The manager endpoint already has the usecase-facing shape. The missing part is wiring a real `MessageRetentionOperator` in `internalv2/app` and exposing the required clusterv2/channelv2 capabilities.

## Boundary Advance Flow

1. Validate `(channel_id, channel_type, through_seq)`.
2. Resolve channel runtime metadata from the cluster view.
3. If the current node is not channel leader, forward once to the leader through node RPC.
4. On the leader, read a fresh compaction view:
   - channel epoch;
   - leader epoch;
   - leader ID;
   - lease fence;
   - current `RetentionThroughSeq`;
   - `HW`;
   - `CheckpointHW`;
   - `MinISRMatchOffset`;
   - durable committed replay cursor.
5. Compute:

```text
safe_through_seq = min(
  requested_through_seq,
  HW,
  CheckpointHW,
  MinISRMatchOffset,
  durable CommittedReplayCursor
)
```

6. If `safe_through_seq <= current RetentionThroughSeq`:
   - return `noop` when the requested boundary is already compacted;
   - return `blocked` when a safety gate is the limiting cause.
7. If `dry_run == true`, return `would_advance` with `advanced_through_seq = safe_through_seq`.
8. For real requests, propose `AdvanceChannelRetentionThroughSeq` through slot metadata with the runtime fences.
9. Apply the committed boundary to local channelv2 runtime/read-floor state.
10. Return `advanced_through_seq = safe_through_seq` and `min_available_seq = safe_through_seq + 1`.

The operation must not call message row delete or range-delete APIs.

## Read Path Requirements

Every committed message read must respect:

```text
min_available_seq = max(RetentionThroughSeq + 1, 1)
```

Required behavior:

- Forward range reads clamp `from_seq` up to `min_available_seq`.
- Reverse/latest reads filter out compacted messages and continue looking for the newest visible message.
- Direct sequence reads for `seq <= RetentionThroughSeq` behave as not found.
- Message ID and client message number lookups must filter the returned row by `message_seq >= min_available_seq`, even if an index still points at a compacted row.
- Manager message query responses must not include compacted rows.
- Gateway message sync must not return compacted rows.
- Conversation/latest-message projections must not surface a latest message that is below the compaction boundary. If the stored latest message is compacted, the projection should behave as having no visible latest message until a newer committed message is available.
- Committed replay starts from `max(cursor + 1, min_available_seq)` and must not attempt to replay compacted messages.

For the first version, runtime can keep physical rows in storage. The logical read floor is still authoritative.

## Failure Semantics

- Invalid channel selector or `through_seq == 0`: `400 bad_request`.
- No known channel leader: service unavailable.
- Request reaches a non-leader node with a newer leader known: forward or return retryable not-leader information.
- Stale epoch, stale leader epoch, or lease mismatch during metadata advance: retryable service unavailable.
- Requested boundary already compacted: `status: "noop"`.
- Safety gates block all progress: `status: "blocked"` with one of:
  - `replay_cursor`;
  - `min_isr_match_offset`;
  - `hw`;
  - `checkpoint_hw`;
  - `current_boundary`.
- Dry run returns the same calculated safe boundary without mutating metadata or runtime state.

Blocked safety status should be a successful manager response, not a transport failure, because operators need to see which gate is lagging.

## Phase 1: Logical Boundary And Read Floor

Purpose: make channelv2 understand the authoritative compaction floor and hide compacted entries. No manager write path is required for this phase if tests inject metadata directly.

Main changes:

- Add `RetentionThroughSeq` and derived `MinAvailableSeq` to channelv2 runtime metadata/projection if not already present.
- Add a channelv2 runtime view for compaction-related watermarks.
- Make channelv2 committed reads clamp/filter by `MinAvailableSeq`.
- Make manager message readers in `internalv2/infra/cluster` respect the same floor.
- Make gateway/message sync usecase paths unable to return compacted rows.
- Update `pkg/channelv2/FLOW.md`, because it currently describes channelv2 without retention/compaction semantics.

Verification focus:

- Inject `RetentionThroughSeq = 2` for a channel with messages `1..4`.
- Manager reads never return `1..2`.
- Message sync never returns `1..2`.
- Latest read returns `4`, not compacted entries.
- Direct lookup of sequence `2` is not found.
- New append continues at `5`; logical compaction does not reset `LEO`.

## Phase 2: Manager-Initiated Boundary Advance

Purpose: wire the existing manager retention API to a real internalv2 cluster-authoritative operator.

Main changes:

- Add a `MessageRetentionOperator` implementation under `internalv2/app`.
- Wire `opts.MessageRetention` in the internalv2 manager composition root.
- Add clusterv2 node methods for:
  - reading a channel compaction view;
  - proposing `AdvanceChannelRetentionThroughSeq`;
  - applying the committed read floor to local channelv2 runtime.
- Add node RPC for forwarding manager requests to the channel leader.
- Keep the usecase contract in `internalv2/usecase/management` cluster-agnostic.

Safety gate calculation must use:

```text
min(requested, HW, CheckpointHW, MinISRMatchOffset, durable CommittedReplayCursor)
```

Verification focus:

- Local leader advances from `0` to safe boundary.
- Remote request forwards to the leader.
- Dry run reports safe boundary without mutation.
- Boundary clamps to each individual gate.
- Boundary no-ops when requested `through_seq <= current RetentionThroughSeq`.
- Stale leader/epoch fences do not advance metadata.

## Later Phase: Replication Catch-Up Semantics

Physical trim cannot be added safely until followers below the compaction boundary have a clear catch-up path.

Later protocol requirements:

- A follower fetch below `RetentionThroughSeq + 1` receives an explicit compacted/reset response.
- The response carries `RetentionThroughSeq` and `MinAvailableSeq`.
- The follower durably adopts the boundary before continuing from `MinAvailableSeq`.
- A follower below the boundary cannot join ISR or become leader until adoption and catch-up complete.
- This is distinct from snapshot install. Snapshot still owns `LogStartOffset`; compaction uses `RetentionThroughSeq`.

## Later Phase: Physical Trim

Once catch-up semantics exist, storage can asynchronously delete physical rows `<= RetentionThroughSeq`.

Required gates:

- `boundary <= HW`;
- `boundary <= CheckpointHW`;
- local store has durably adopted the boundary;
- duplicate/idempotency indexes are deleted consistently with message rows;
- `LEO` recovery uses a retained max sequence or local retention state so prefix deletion cannot make the end of log regress.

Physical trim remains an implementation detail of local storage. It cannot decide logical visibility.

## Testing Plan

Backend unit tests:

- `internalv2/access/manager`: request validation, permission mapping, response/error JSON.
- `internalv2/usecase/management`: request validation and port delegation.
- `internalv2/app`: local leader advance, remote forwarding, dry run, clamping, no-op, blocked reasons, stale fence failures.
- `internalv2/access/node`: retention RPC codec round trip and leader-only handler behavior.
- `internalv2/infra/cluster`: manager message reads respect compaction floor.
- `pkg/clusterv2`: metadata advance proposes the slot FSM command and preserves monotonic boundaries.
- `pkg/channelv2`: forward reads, reverse reads, direct lookups, latest reads, and append-after-compaction behavior.

Black-box e2e candidate:

1. Start a three-node `wukongimv2` cluster.
2. Send messages with sequences `1..4`.
3. Advance compaction through `2`.
4. Verify manager query and message sync only return `3..4`.
5. Send message `5`.
6. Verify sequences continue monotonically and old messages remain hidden after restart/leader change.

Focused commands:

```sh
GOWORK=off /usr/local/go/bin/go test ./pkg/channelv2/... ./pkg/clusterv2/... ./internalv2/... -count=1
GOWORK=off /usr/local/go/bin/go test -tags=e2e ./test/e2ev2/message -run 'Compaction|Retention|Message' -count=1
```

The e2e command should be added when the black-box scenario exists; normal development should begin with the focused unit packages.

## Documentation Updates During Implementation

- Update `pkg/channelv2/FLOW.md` once Phase 1 lands.
- Update any touched `internalv2/access/manager`, `internalv2/usecase/management`, `internalv2/infra/cluster`, or `internalv2/app` `FLOW.md` files if their described flow changes.
- `docs/development/PROJECT_KNOWLEDGE.md` already records that channel message retention is cluster-authoritative and manager history deletion must advance `RetentionThroughSeq`; add an internalv2-specific note only if implementation introduces a new rule.

## Review Points

- Confirm the manager API should stay named `retention` even though the internal model is log compaction.
- Confirm first release should include Phase 1 and Phase 2 only, with no physical deletion.
- Confirm whether gateway message sync and conversation/latest-message behavior should be included in the same implementation PR or split after the manager query path.
