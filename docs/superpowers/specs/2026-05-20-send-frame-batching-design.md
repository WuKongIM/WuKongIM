# Send Frame Batching Design

## Goal

Increase SEND path QPS by batching adjacent `SendPacket` frames before durable append, while preserving strict per-channel ordering and the existing client protocol.

## Context

The current path handles one SEND frame at a time:

```text
gateway async shard worker
  -> access/gateway handleSend
  -> message.App.Send
  -> channel.AppendRequest{Message: one}
  -> handler.Append
  -> replica.Append([]Record{one})
  -> leader durable append
  -> follower fetch/apply
  -> quorum wait
  -> write Sendack
```

The lower layers already support records in batches:

- `pkg/channel/replica.Append(ctx, []channel.Record)`
- `pkg/channel/store.ChannelStore.Append([]channel.Record)`
- follower `ApplyFollowerBatch`
- cross-channel Pebble `commitCoordinator`

The missing piece is that upper layers do not feed batches into the channel append path. When a single SEND takes about 200 ms to reach quorum, a synchronous per-frame worker prevents same-channel pending appends from accumulating.

## Non-Goals

- Do not change the wire protocol.
- Do not add a new client-facing batch SEND frame in this phase.
- Do not weaken per-channel strict ordering.
- Do not introduce a non-cluster single-node path.
- Do not bypass existing permission, hook, idempotency, write fence, epoch, or commit-mode semantics.

## Recommended Architecture

Use an internal micro-batch pipeline:

```text
gateway async SEND shard
  -> micro-batch drain
  -> access gateway SendBatch adapter
  -> message.App.SendBatch
  -> channel.AppendBatch per channel segment
  -> replica.Append(ctx, []Record)
  -> existing leader/follower durable batch path
  -> per-item Sendack
```

### Gateway Micro-Batcher

`internal/gateway/core` keeps `ChannelID + ChannelType` sharding. Each shard worker drains a bounded micro-batch instead of dispatching only one SEND task.

Flush conditions:

- `GatewaySendBatchMaxWait`: default 500 us.
- `GatewaySendBatchMaxRecords`: default 128.
- `GatewaySendBatchMaxBytes`: default 512 KiB.
- shutdown or shard close.

After receiving the first item, the worker waits up to `GatewaySendBatchMaxWait` for near-future SEND frames unless record or byte limits fire first. It must not flush immediately just because the queue is briefly empty; that would preserve the current one-frame behavior for hot channels.

The worker processes one batch at a time. Raw gateway sharding is a batching hint and a concurrency limiter, not the final ordering authority. Strict order is ultimately enforced by channel-level append serialization after business channel normalization.

Each gateway batch item must carry:

- original `ReplyToken`
- session/context state
- original `SendPacket`
- original index in the batch
- enqueue time
- approximate payload byte size

### Access Gateway Batch Adapter

`internal/access/gateway` adds a SEND-specific batch entrypoint. It should not become a generic frame batch API because SEND has durable ACK semantics and per-item errors.

Responsibilities:

- decrypt each `SendPacket` independently.
- map valid packets to `message.SendCommand`.
- produce item-level rejections for decrypt/map/auth errors.
- call `message.App.SendBatch` for valid commands.
- write one Sendack per original item.
- preserve each item's original `ReplyToken`.
- keep result alignment with the input order.
- return only infrastructure errors that should still flow through gateway core handler-error handling.

### Message SendBatch

`internal/usecase/message` adds:

```go
type SendBatchItem struct {
    Context context.Context
    Command SendCommand
}

func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult
```

`Send` becomes a one-item wrapper around the batch implementation where practical.

The API is item-context-aware. A single canceled session or expired request must not cancel unrelated items. Each item uses its own request context and send timeout. A durable segment may share one append context only after filtering out expired items and deriving a context that cannot outlive any included item deadline.

Per-item behavior remains unchanged:

- sender auth
- user send limiter
- channel type validation
- person/agent channel normalization
- permission checks
- plugin `BeforeSend`
- realtime `NoPersist` handling
- request-scoped CMD handling
- committed dispatcher submission

Durable commands are split into ordered channel segments by:

- canonical normalized `ChannelID`
- `ChannelType`
- `CommitMode`
- `ExpectedChannelEpoch`
- `ExpectedLeaderEpoch`
- `SupportsMessageSeqU64`

The segment split must preserve input order for each channel. A later command for the same channel must never overtake an earlier command.

### Channel AppendBatch

`pkg/channel` adds batch append request/result types. `pkg/channel/handler` implements the same checks as the single append path, but only once per segment when possible.

Core flow:

1. validate metadata, epoch expectation, channel status, protocol sequence format, runtime existence, and leader role.
2. resolve stored idempotency per message before write-fence checks, matching current single-append semantics.
3. coalesce in-batch idempotency duplicates before records reach `store.messageTable.append`.
4. apply write-fence checks only after idempotent duplicates have been resolved.
5. compute legacy U32 sequence capacity before durable append; fail or split tail items that cannot receive a valid sequence.
6. allocate message IDs only for new messages.
7. encode new messages into `[]channel.Record`.
8. call `group.Append(ctx, records)` once for the remaining new records.
9. map returned base offset back to per-item message sequences.
10. re-read channel status after append, matching current single append behavior.
11. return results aligned with request order.

In-batch idempotency rules:

- same `ChannelID + FromUID + ClientMsgNo` and same payload: first item appends, later items return the first item's result.
- same idempotency key and different payload: later conflicting items fail with `ErrIdempotencyConflict`.
- no duplicate idempotency key reaches `store.messageTable.append`, because the store currently treats duplicate idempotency keys inside one multi-row write as corruption.

Duplicate stored idempotency hits should succeed without re-append and must bypass an active write fence, as the existing single append path does. Segment-level durable errors should fail the segment's new messages and leave already resolved duplicate hits intact.

### Remote Leader Forwarding

The batch design must cover follower-ingress sends, not only leader-local sends. Add batch-capable internal APIs across the existing local/remote boundary:

- `message.ChannelBatchAppender`
- `message.RemoteBatchAppender`
- app channel-cluster wrapper batch local/forward path
- node RPC batch request/response codec and handler
- stale metadata, redirect, lease, and write-fence retry behavior at segment granularity

If a remote peer does not support batch forwarding during rollout, the safe fallback is de-batching over the existing single append RPC. The fallback preserves correctness but should be visible in trace/logs because it limits multi-node QPS gains.

### Replica, Store, and Follower Path

The lower layer should be reused, not redesigned:

- leader `replica.Append` already accepts multiple records.
- store append already writes multiple rows into one Pebble batch.
- follower fetch/apply already supports batch transfer.
- quorum wait already targets the last offset in the append range.

The main work here is verification and tuning, not new APIs.

## Ordering Guarantees

The design preserves strict channel order through these rules:

- hash SEND tasks by raw `ChannelID + ChannelType` as an admission/batching hint.
- one worker consumes each shard serially.
- batch drain does not reorder tasks.
- `SendBatch` normalizes person/agent channel IDs before durable segmentation.
- `SendBatch` preserves canonical per-channel order when creating segments.
- `AppendBatch` assigns contiguous message sequences in segment order.
- Sendack output is aligned to original task order.

Because person and agent sends can map different raw packet channel IDs to the same canonical channel, gateway sharding alone is not treated as proof of strict order. Tests must cover canonical normalization ordering.

## Backpressure and Failure Handling

- Gateway queues remain bounded. If a shard queue is full, keep the existing async queue full close/reject behavior.
- Each batch has max records and max bytes to bound per-batch work. This does not bound the total queued cloned payload memory; queue capacity and payload-size limits remain the queue-level memory controls.
- Expired item contexts are failed before durable append.
- Durable retry follows existing stale-meta / not-leader retry behavior at segment granularity.
- A batch must never wait for an item longer than the existing send timeout.
- Shutdown closes queues and drains or fails pending batch items deterministically.

## Configuration

Add gateway/internal batching config with conservative defaults:

```text
WK_GATEWAY_SEND_BATCH_MAX_WAIT=500us
WK_GATEWAY_SEND_BATCH_MAX_RECORDS=128
WK_GATEWAY_SEND_BATCH_MAX_BYTES=524288
```

Configuration work includes app config fields, strict parser tests, explicit flag handling for invalid values, English comments, and `wukongim.conf.example` updates for all three keys.

Also tune existing channel/store settings for acceptance tests:

```text
AppendGroupCommitMaxRecords >= 128
AppendGroupCommitMaxBytes >= 512KB
CommitCoordinatorMaxRecords >= 512
CommitCoordinatorMaxBytes >= 1MB
DataPlaneMaxFetchInflight >= 16
DataPlaneMaxPendingFetch >= 16
```

## Observability

Use existing trace fields where possible (`RequestCount`, `RecordCount`, `ByteCount`, and `QueueDepth`) and add new emission points only where needed:

- gateway batch collect wait
- gateway batch size records/bytes
- message durable segment size
- channel append batch records/bytes
- follower fetch/apply records per response
- per-item Sendack latency

The key acceptance signal is that `replica.leader.durable_append_store` and `store.commit.pebble_sync` show `RecordCount > 1` under stress.

## Test Strategy

Unit tests:

- gateway shard micro-batcher drains multiple SEND tasks without reordering.
- same `ChannelID` tasks always map to one shard.
- batch handler returns Sendacks aligned to input order.
- `SendBatch` splits ordered channel segments correctly.
- `AppendBatch` assigns contiguous sequences.
- idempotency duplicate/conflict behavior remains compatible.
- in-batch duplicate idempotency returns the first result.
- in-batch idempotency conflict fails only later conflicting items.
- stored duplicate idempotency bypasses write fence.
- legacy U32 sequence exhaustion is detected before durable append.
- item timeout and batch durable failure behavior are deterministic.
- mixed-session cancellation does not cancel unrelated batch items.
- person/agent canonical normalization preserves channel ordering.
- follower-ingress batch forwarding works or explicitly falls back to de-batching.

Integration tests:

- single-node cluster SEND batch stress.
- three-node high-channel SEND batch stress.
- single hot channel stress to prove same-channel batching increases QPS.
- trace assertions that durable append/store sync batch sizes exceed one.

## Expected Impact

If a quorum append round takes about 200 ms, the current path effectively amortizes that cost over one message. With a batch of 64 messages, the same round can commit 64 messages. Actual QPS will be bounded by encoding, permissions, ACK writes, follower fetch/apply, and disk sync, but this should produce a much larger improvement than only increasing channel cardinality.
