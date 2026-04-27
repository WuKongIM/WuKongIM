# Cluster Idle CPU Code Optimization Design

- Date: 2026-04-27
- Scope: `pkg/slot/multiraft`, `pkg/cluster`, `pkg/transport`, `pkg/metrics`
- Goal: reduce three-node cluster idle CPU toward `<1%` per `wk-node` without slowing request path or changing Raft tick/election configuration.

## 1. Background

A three-node local cluster with 10 managed slots shows each `wk-node` at roughly `5%~6%` CPU while business traffic is near idle. Fresh pprof and metrics show the residual cost is dominated by always-on Raft and transport work rather than application messages:

- `pkg/transport.(*MuxConn).readLoop`
- `pkg/transport.(*priorityWriter).loop`
- `pkg/slot/multiraft.(*Runtime).processSlot`
- `pkg/slot/multiraft.(*slot).processReady`
- `go.etcd.io/raft/v3.(*RawNode).Status`
- `pkg/slot/multiraft.(*slot).refreshStatus`
- `pkg/cluster.(*raftTransport).Send`

The current idle path sends many small Raft heartbeat frames across the transport and repeatedly refreshes slot status using `RawNode.Status()`, which allocates and clones raft progress even though the runtime only needs basic fields.

## 2. Goals

1. Keep current Raft timing semantics and avoid slowing normal requests.
2. Reduce idle transport frame count for Raft heartbeats and empty steady-state Ready traffic.
3. Remove avoidable `RawNode.Status()` allocation/cloning from the hot path.
4. Reduce Prometheus label lookup cost on transport send/receive metrics.
5. Preserve existing public APIs, deployment shape, and single-node cluster semantics.
6. Keep focused tests fast and unit-level where possible.

## 3. Non-goals

- Do not lower `WK_CLUSTER_TICK_INTERVAL`, raise election timeout, or otherwise depend on slower configuration.
- Do not redesign Raft election or CheckQuorum behavior.
- Do not change application send semantics or routing behavior.
- Do not replace the transport protocol globally for RPC traffic.
- Do not remove existing metrics or rename existing metric labels.

## 4. Proposed Design

### 4.1 Lightweight Slot Status Refresh

Replace the hot-path call to `RawNode.Status()` with `RawNode.BasicStatus()` in `pkg/slot/multiraft`.

Current status refresh needs only:

- leader id
- term
- commit index
- applied index
- raft role

`BasicStatus()` provides those fields without the Progress map clone that `Status()` performs. The slot keeps the same cached `Status` struct and the same leadership-dependent failure behavior.

Implementation shape:

- Add or modify the status refresh helper in `pkg/slot/multiraft/slot.go` to use `g.rawNode.BasicStatus()`.
- Continue mapping raft role through the existing `mapRole` helper.
- Keep `failLeadershipDependentLocked(ErrNotLeader)` when the cached role transitions from leader to non-leader.
- Avoid the duplicate refresh after `processReady()` already refreshed status.

Expected result: `go.etcd.io/raft/v3.getStatus`, `tracker.Config.Clone`, and related allocation cost should disappear from the idle profile.

### 4.2 Raft Batch Transport Frame

Add a cluster-specific batch frame for Slot Raft messages. This preserves the transport API and avoids changing RPC traffic.

Wire types:

- Existing `msgTypeRaft = 1` remains supported for single-message compatibility and tests.
- New `msgTypeRaftBatch = 4` carries multiple Slot Raft messages for one target node.

Batch body format:

```text
[count:uint32]
repeated count times:
  [slot_id:uint64]
  [message_len:uint32]
  [message_bytes:message_len]
```

Send path:

- `pkg/cluster.(*raftTransport).Send(ctx, []multiraft.Envelope)` groups envelopes by `env.Message.To`.
- Each target node receives at most one batch body per call.
- Each envelope still contains the original raft message bytes produced by `env.Message.Marshal()`.
- Transient send failures keep current semantics: log/skip as needed and rely on Raft retransmission.
- Self/invalid target behavior remains unchanged.

Receive path:

- Register `msgTypeRaftBatch` with the same transport server as `msgTypeRaft`.
- Decode each record and call a shared internal helper that routes one `(slotID, raft message bytes)` pair into `runtime.Step()`.
- If the batch body is malformed, reject the batch and log a warning/debug entry; do not panic.
- Keep the legacy single-frame handler for `msgTypeRaft`.

Expected result: idle heartbeat traffic changes from many per-slot frames to one frame per peer per ready batch. This should reduce `readFrame`, `priorityWriter.loop`, `WriteTo`, and syscall samples without changing Raft timing.

### 4.3 Transport Metrics Hot Path Cache

Keep all existing transport metric names and labels, but avoid repeated `CounterVec.WithLabelValues` lookup for common transport message types.

Implementation shape:

- Extend `pkg/metrics.TransportMetrics` with cached `prometheus.Counter` handles for common send/receive message labels:
  - `"1"` for slot raft
  - `"3"` for controller raft
  - `"4"` for slot raft batch
  - `"rpc_request"`
  - `"rpc_response"`
- `ObserveSentBytes(msgType string, bytes int)` and `ObserveReceivedBytes(msgType string, bytes int)` first check the cache and fallback to the existing vector path for uncommon message labels.
- Keep existing tests that assert metric output, and add a cache-path test if needed.

Expected result: metrics overhead should no longer be visible in per-frame pprof hot paths.

## 5. Data Flow

```text
Slot runtime tick
  -> rawNode.Tick()
  -> rawNode.Ready()
  -> ready.Messages
  -> wrapMessagesInto([]Envelope)
  -> raftTransport.Send()
  -> group envelopes by target node
  -> encode msgTypeRaftBatch body
  -> transport.Client.Send(target, msgTypeRaftBatch)
  -> peer transport.Server dispatch
  -> decode batch records
  -> handle one raft record
  -> runtime.Step(slotID, raftpb.Message)
```

Slot status updates remain local:

```text
processSlot/processReady
  -> refresh cached Status from RawNode.BasicStatus()
  -> Runtime.Status(slotID) returns cached Status snapshot
```

## 6. Error Handling

- Batch encode failures from raft message marshal return an error to the transport sender, matching the current `Send` method contract.
- Per-target transport send errors remain non-fatal for Raft and are skipped/logged according to current semantics.
- Batch decode rejects malformed input with an error and logs once for that batch.
- A decoded raft message that targets a missing slot follows the current `runtime.Step` behavior and is ignored/logged as today.
- Existing single-frame Raft handler remains the fallback and test compatibility path.

## 7. Testing Strategy

### Unit Tests

- `pkg/slot/multiraft`
  - Verify status refresh still updates leader, term, commit, applied, and role.
  - Verify leadership-dependent futures still fail when a local leader becomes non-leader.

- `pkg/cluster`
  - Verify raft batch encode/decode round trip for multiple slots.
  - Verify malformed batch bodies fail cleanly.
  - Verify batch handler dispatches all records to the runtime step path.
  - Verify `raftTransport.Send` groups multiple envelopes for the same target into one send.
  - Verify legacy `msgTypeRaft` handling still works.

- `pkg/metrics`
  - Verify cached send/receive byte counters produce the same metric values as the existing public methods.

### Focused Test Command

```bash
go test ./pkg/slot/multiraft ./pkg/cluster ./pkg/metrics
```

### Runtime Verification

After implementation, run a three-node docker compose cluster and collect fresh idle data:

```bash
docker stats --no-stream

go tool pprof -top -nodecount=40 'http://127.0.0.1:15001/debug/pprof/profile?seconds=30'
go tool pprof -top -nodecount=40 'http://127.0.0.1:15002/debug/pprof/profile?seconds=30'
go tool pprof -top -nodecount=40 'http://127.0.0.1:15003/debug/pprof/profile?seconds=30'
```

Success criteria:

- Idle CPU per `wk-node` approaches or falls below `1%`.
- `RawNode.Status/getStatus` is no longer a meaningful pprof hotspot.
- `MuxConn.readLoop` and `priorityWriter.loop` samples drop materially.
- Existing focused tests pass.
- No request latency regression is introduced by slower configuration, because no slower configuration is used.

## 8. Risks and Mitigations

- Risk: batch frame decoding introduces a new wire format bug.
  - Mitigation: encode/decode round-trip tests and malformed input tests.

- Risk: batching hides per-message transport metrics.
  - Mitigation: existing byte metrics remain frame-level metrics; add message-count metrics only in a later scoped change if needed.

- Risk: grouping by target changes send ordering.
  - Mitigation: preserve envelope order within each target group. Cross-target ordering is not guaranteed today and is not semantically required.

- Risk: removing duplicate status refresh delays visible status updates.
  - Mitigation: keep one refresh after Ready processing and one lightweight refresh for tick/no-ready paths.

## 9. Rollout Notes

This is a code-level optimization. It must not require operators to change `WK_CLUSTER_TICK_INTERVAL`, `WK_CLUSTER_ELECTION_TICK`, or `WK_CLUSTER_HEARTBEAT_TICK`.

If the first phase does not reach the idle CPU target, the next code-level step should be scheduler-level batching in `pkg/slot/multiraft` rather than configuration slowdown.
