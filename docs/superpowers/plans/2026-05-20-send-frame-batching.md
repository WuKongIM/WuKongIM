# Send Frame Batching Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add internal SEND micro-batching so one channel append can durably commit multiple SEND frames while preserving strict per-channel ordering and existing client protocol semantics.

**Architecture:** Gateway async SEND shards collect bounded micro-batches and call a SEND-specific batch interface. The access gateway maps per-frame validation results to aligned item results, `message.App.SendBatch` groups durable commands into ordered channel segments, and `pkg/channel/handler.AppendBatch` turns same-channel segments into one `replica.Append(ctx, []Record)`.

**Tech Stack:** Go, existing WuKongIM gateway/session abstractions, `pkg/channel` replica/store batch append path, Go unit tests, integration send stress tests.

---

## File Map

- Modify `internal/gateway/types/options.go`
  - Add gateway SEND batch knobs to session/server options if they belong near async dispatch options.
- Modify `internal/gateway/core/server.go`
  - Replace per-task async SEND dispatch with bounded micro-batch collection.
  - Add batch-capable dispatch path while retaining single-frame fallback for handlers that do not implement batch.
- Modify `internal/gateway/core/async_dispatch_test.go`
  - Test batch drain limits, same-channel ordering, queue full behavior, and trace wait preservation.
- Modify `internal/gateway/core/server_test.go`
  - Test end-to-end gateway dispatch order across batches and non-SEND frames.
- Modify `internal/access/gateway/handler.go`
  - Extend `MessageUsecase` with optional batch use through a separate interface.
- Modify `internal/access/gateway/frame_router.go`
  - Add SEND batch handler that validates each item and writes aligned Sendacks.
- Modify `internal/access/gateway/handler_test.go`
  - Test batch item alignment, per-item rejection, timeout mapping, and Sendack order.
- Modify `internal/usecase/message/command.go`
  - Add `SendBatchItemResult` and internal segment helper types if needed.
- Modify `internal/usecase/message/deps.go`
  - Extend `ChannelCluster` and `RemoteAppender` with batch-capable optional interfaces.
- Modify `internal/usecase/message/send.go`
  - Add `SendBatch`; make `Send` a one-item wrapper where safe.
  - Add ordered durable segment splitting.
- Modify `internal/usecase/message/retry.go`
  - Add batch-aware metadata refresh/retry helper or keep retry at segment level.
- Modify `internal/access/node/channel_append_rpc.go`
  - Add internal node RPC request/response handling for batch append forwarding.
- Modify node RPC codec/client files found by `rg "AppendToLeader|channel_append" internal/access pkg internal/app`.
  - Add batch forwarding codec and client methods or explicit de-batch fallback.
- Modify `internal/usecase/message/send_test.go`
  - Test `SendBatch` validation, segmentation, order, idempotent result alignment, and dispatcher submission.
- Modify `pkg/channel/types.go`
  - Add `AppendBatchRequest`, `AppendBatchResult`, and item result types with English comments.
- Modify `pkg/channel/handler/append.go`
  - Add `AppendBatch` and refactor single append to call shared batch logic.
- Modify `pkg/channel/handler/append_test.go`
  - Test contiguous sequence assignment, idempotency duplicate/conflict, write fence, stale meta, and U64 checks.
- Modify `pkg/channel/api_test.go`
  - Add public API coverage for batch append if the service interface is exposed there.
- Modify `internal/app/config.go`
  - Add documented `WK_GATEWAY_SEND_BATCH_*` config fields if gateway options are app-configured.
- Modify `internal/app/config_test.go`
  - Validate defaults, env parsing, explicit invalid values, and example config alignment.
- Modify `internal/app/build.go`
  - Wire config into gateway server/session options.
- Modify `cmd/wukongim/config.go`
  - Keep generated/default config display or config key registration aligned if this file owns CLI config metadata.
- Modify `wukongim.conf.example`
  - Add the new `WK_GATEWAY_SEND_BATCH_*` keys if config fields are added.
- Modify `internal/app/comm_test.go`
  - Add stress preset tuning for batch SEND acceptance if needed.
- Modify `internal/app/send_stress_integration_test.go`
  - Add assertions/logs for batch trace stats.
- Modify `docs/development/PROJECT_KNOWLEDGE.md`
  - Record concise batching knowledge after implementation is verified.

## Implementation Notes

- Use TDD for every behavior change.
- Do not change the wire protocol.
- Preserve all existing single-send behavior.
- Keep batch APIs internal unless an existing public channel interface needs extension.
- Keep per-channel order stronger than throughput optimizations.
- Resolve stored idempotency before write-fence checks, matching current `Append`.
- Coalesce duplicate idempotency keys inside one batch before records reach `store.messageTable.append`.
- Preserve item-level request contexts; one canceled session must not cancel unrelated batch items.
- Treat raw gateway sharding as a batching hint only; canonical channel order is enforced after message normalization and channel append serialization.
- Do not run destructive git commands.
- Commit steps are listed as checkpoints; run them only if the human explicitly wants commits in this working tree.

---

## Task 1: Gateway Batch Options and Collector

**Files:**
- Modify: `internal/gateway/types/options.go`
- Modify: `internal/gateway/core/server.go`
- Test: `internal/gateway/core/async_dispatch_test.go`

- [ ] **Step 1: Write failing tests for batch option normalization**

Add tests near existing async worker tests:

```go
func TestAsyncSendBatchOptionsUseDefaults(t *testing.T) {
    opt := gatewaytypes.NormalizeSessionOptions(gatewaytypes.SessionOptions{})
    require.Equal(t, 500*time.Microsecond, opt.AsyncSendBatchMaxWait)
    require.Equal(t, 128, opt.AsyncSendBatchMaxRecords)
    require.Equal(t, 512*1024, opt.AsyncSendBatchMaxBytes)
}

func TestAsyncSendBatchOptionsCanDisableWaitButNotBounds(t *testing.T) {
    opt := gatewaytypes.NormalizeSessionOptions(gatewaytypes.SessionOptions{
        AsyncSendBatchMaxWait:    -time.Microsecond,
        AsyncSendBatchMaxRecords: -1,
        AsyncSendBatchMaxBytes:   -1,
    })
    require.Zero(t, opt.AsyncSendBatchMaxWait)
    require.Equal(t, 128, opt.AsyncSendBatchMaxRecords)
    require.Equal(t, 512*1024, opt.AsyncSendBatchMaxBytes)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendBatchOptions' -count=1
```

Expected: compile failure because the option fields do not exist.

- [ ] **Step 3: Add option fields and defaults**

In `internal/gateway/types/options.go`, add English comments:

```go
// AsyncSendBatchMaxWait bounds how long a SEND shard waits to collect adjacent frames.
AsyncSendBatchMaxWait time.Duration
// AsyncSendBatchMaxRecords caps SEND frames in one gateway micro-batch.
AsyncSendBatchMaxRecords int
// AsyncSendBatchMaxBytes caps payload bytes in one gateway micro-batch.
AsyncSendBatchMaxBytes int
```

Update normalization with:

```go
const (
    defaultAsyncSendBatchMaxWait    = 500 * time.Microsecond
    defaultAsyncSendBatchMaxRecords = 128
    defaultAsyncSendBatchMaxBytes   = 512 * 1024
)
```

Rules:

- zero wait means default.
- negative wait means no wait.
- non-positive records/bytes use defaults.

- [ ] **Step 4: Run option tests to verify green**

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendBatchOptions' -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing tests for collector limits**

In `internal/gateway/core/async_dispatch_test.go`, add tests that create `newAsyncDispatchQueueWithCapacity`, enqueue tasks, and call a new helper such as `collectAsyncSendBatch(first, tasks, limits)`.

Required cases:

- collects up to `MaxRecords`.
- stops before exceeding `MaxBytes`.
- preserves task order.
- with `MaxWait=0`, drains only immediately available tasks.
- with `MaxWait>0`, waits for a near-future second task before flushing.
- when a received task would exceed `MaxBytes`, keeps it as a shard-local pending task for the next batch instead of dropping or reordering it.

- [ ] **Step 6: Run collector tests to verify they fail**

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendBatchCollect' -count=1
```

Expected: compile failure because collector helper/types do not exist.

- [ ] **Step 7: Implement minimal collector**

In `internal/gateway/core/server.go`, add:

```go
type asyncSendBatchLimits struct {
    maxWait    time.Duration
    maxRecords int
    maxBytes   int
}

func collectAsyncSendBatch(first asyncDispatchTask, tasks <-chan asyncDispatchTask, limits asyncSendBatchLimits) []asyncDispatchTask
```

Implementation rules:

- always include `first`.
- use `send.Payload` length for byte accounting.
- if the next task would exceed byte limit, process it in the next batch; since channels cannot unread, avoid over-reading by checking only after receiving is not possible. Prefer a shard-local pending slot if needed.
- wait up to `maxWait` after the first task so hot-channel near-arrival traffic can batch.
- use a shard-local pending slot for an over-limit task; do not drop, duplicate, or reorder it.

- [ ] **Step 8: Run collector tests to verify green**

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendBatchCollect' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 2: Gateway Batch Dispatch Interface

**Files:**
- Modify: `internal/gateway/types/handler.go` or the file defining gateway handler interfaces
- Modify: `internal/gateway/core/server.go`
- Test: `internal/gateway/core/async_dispatch_test.go`
- Test: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Locate handler interface**

Run:

```bash
rg -n "type Handler interface|OnFrame" internal/gateway/types internal/gateway -g'*.go'
```

Expected: find the current handler interface used by `core/dispatcher.go`.

- [ ] **Step 2: Write failing test for batch-capable handler**

Add a test with a fake handler implementing:

```go
type SendBatchHandler interface {
    OnSendBatch(items []gateway.SendBatchItem) error
}
```

The exact shape may be adjusted, but test must prove:

- two SEND frames in the same shard call the batch method once.
- `OnFrame` is not called for those SEND frames.
- input order is preserved.

- [ ] **Step 3: Run test to verify fail**

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendDispatchUsesBatchHandler' -count=1
```

Expected: compile failure because batch interface does not exist.

- [ ] **Step 4: Add SEND batch interface**

Add a small optional interface in the appropriate gateway package:

```go
type SendBatchItem struct {
    Context    *Context
    ReplyToken string
    Frame      *frame.SendPacket
    Index      int
    EnqueuedAt time.Time
    ByteCount  int
}

type SendBatchHandler interface {
    OnSendBatch(items []SendBatchItem) error
}
```

Use the same package that owns `Context` and `Handler`.

- [ ] **Step 5: Dispatch batches when supported**

In `runAsyncDispatchWorker`:

- receive first task.
- collect batch.
- if handler implements `SendBatchHandler`, build item contexts and call it once.
- otherwise dispatch each task with existing `dispatchFrame`.
- call `recordAsyncDispatchWait` for every item.
- preserve each task's original reply token in its batch item.
- if `OnSendBatch` returns an infrastructure error, feed it through existing `handleHandlerError` behavior once for the affected session; per-item send failures should be converted by access gateway into Sendacks.

- [ ] **Step 6: Preserve fallback behavior**

Add/keep tests that a handler without `SendBatchHandler` still receives each SEND via `OnFrame`.

Run:

```bash
go test ./internal/gateway/core -run 'TestAsyncSendDispatch(UsesBatchHandler|FallsBackToFrameHandler)' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 3: Access Gateway SEND Batch Adapter

**Files:**
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/frame_router.go`
- Test: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write failing test for aligned Sendacks**

Create fake message usecase with:

```go
type batchMessageUsecase struct {
    calls []message.SendBatchItem
    results []message.SendBatchItemResult
}
```

Test:

- send two valid `SendPacket`s.
- call `handler.OnSendBatch`.
- assert message batch called once with two items and each item has its own context.
- assert two Sendacks are written in input order.
- assert each Sendack uses the original reply token.

Run:

```bash
go test ./internal/access/gateway -run 'TestHandlerOnSendBatchWritesAlignedSendacks' -count=1
```

Expected: compile failure because `OnSendBatch` does not exist.

- [ ] **Step 2: Add optional message batch interface**

In `internal/access/gateway/handler.go`, keep `MessageUsecase` unchanged if broad changes are risky and add:

```go
type MessageBatchUsecase interface {
    SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult
}
```

- [ ] **Step 3: Implement `Handler.OnSendBatch`**

In `internal/access/gateway/frame_router.go`:

- iterate items.
- decrypt each packet.
- map each command.
- create item errors for failures.
- create one timeout context per valid item from that item's original request context.
- build one `[]message.SendBatchItem` for valid items.
- call `SendBatch` if available; otherwise loop `Send`.
- write Sendack for every input item.
- do not let one item's canceled context cancel unrelated items.

- [ ] **Step 4: Test per-item rejection**

Add test:

- first item has invalid/missing auth and maps to rejected Sendack.
- second item is valid and still enters batch.
- output has two Sendacks.
- one canceled item does not cancel another valid item in the same batch.

Run:

```bash
go test ./internal/access/gateway -run 'TestHandlerOnSendBatch' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 4: Message SendBatch Skeleton and Segment Splitter

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/send.go`
- Test: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add failing test for one-item Send wrapper**

Test that `Send(ctx, cmd)` and `SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})[0]` return the same result against existing fakes.

Run:

```bash
go test ./internal/usecase/message -run 'TestSendBatchSingleItemMatchesSend' -count=1
```

Expected: compile failure because `SendBatch` result type does not exist.

- [ ] **Step 2: Add result type**

In `command.go`:

```go
type SendBatchItem struct {
    // Context is the per-send request context for cancellation and timeout.
    Context context.Context
    // Command is the normalized or raw send command for this item.
    Command SendCommand
}

type SendBatchItemResult struct {
    Result SendResult
    Err    error
}
```

- [ ] **Step 3: Implement one-item wrapper minimally**

Add:

```go
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult
```

For this task, loop over `a.Send(ctx, cmd)` to make tests pass. Guard against recursion by moving existing `Send` body into `sendOne` before making `Send` call `SendBatch`.

- [ ] **Step 4: Verify one-item behavior**

Run:

```bash
go test ./internal/usecase/message -run 'TestSendBatchSingleItemMatchesSend' -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing tests for durable segment splitting**

Add tests for an unexported helper:

```go
segments := splitDurableSendSegments(preparedItems)
```

Cases:

- same channel and same append options coalesce.
- same channel with different commit mode splits.
- same channel order is preserved.
- different channels produce separate segments without reordering within each channel.
- person and agent sends are split by canonical normalized channel, not raw packet channel.

- [ ] **Step 6: Implement segment helper**

Use a small internal struct:

```go
type durableSendSegment struct {
    key durableSendSegmentKey
    indexes []int
    items []preparedSend
}
```

Do not optimize across a same-channel barrier. Simpler first pass: iterate input order and append to the last segment only if key matches; this preserves order and still batches adjacent frames from gateway shard.

- [ ] **Step 7: Verify message tests**

Run:

```bash
go test ./internal/usecase/message -run 'TestSendBatch|TestSplitDurableSendSegments' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 5: Channel AppendBatch API and Handler

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/handler/append.go`
- Test: `pkg/channel/handler/append_test.go`

- [ ] **Step 1: Write failing test for contiguous sequence assignment**

In `pkg/channel/handler/append_test.go`, add:

```go
func TestAppendBatchAssignsContiguousMessageSeq(t *testing.T) {
    svc, _, _ := newAppendService(t, channel.ChannelID{ID: "batch", Type: 1})
    res, err := svc.AppendBatch(context.Background(), channel.AppendBatchRequest{
        ChannelID: channel.ChannelID{ID: "batch", Type: 1},
        Messages: []channel.Message{
            {FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("one")},
            {FromUID: "u1", ClientMsgNo: "m2", Payload: []byte("two")},
        },
        SupportsMessageSeqU64: true,
    })
    require.NoError(t, err)
    require.Len(t, res.Items, 2)
    require.EqualValues(t, 1, res.Items[0].MessageSeq)
    require.EqualValues(t, 2, res.Items[1].MessageSeq)
}
```

Run:

```bash
go test ./pkg/channel/handler -run 'TestAppendBatchAssignsContiguousMessageSeq' -count=1
```

Expected: compile failure because `AppendBatch` does not exist.

- [ ] **Step 2: Add public batch types**

In `pkg/channel/types.go`, add English comments:

```go
// AppendBatchRequest appends multiple messages to one channel in strict order.
type AppendBatchRequest struct { ... }

// AppendBatchResult returns per-message append results aligned with the request.
type AppendBatchResult struct { Items []AppendBatchItemResult }

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult struct { MessageID uint64; MessageSeq uint64; Message Message; Err error }
```

- [ ] **Step 3: Refactor single append through shared helper**

In `pkg/channel/handler/append.go`:

- keep `Append(ctx, req AppendRequest)` signature.
- implement `AppendBatch(ctx, req AppendBatchRequest)`.
- make `Append` call shared validation/build logic or call `AppendBatch` with one message.
- preserve all existing error mappings.

- [ ] **Step 4: Implement batch append**

Shared logic:

- `metaForKey`
- epoch compatibility
- status checks
- sequence format check
- runtime leader check
- commit mode context
- stored idempotency lookup per item before write fence, matching current single append.
- in-batch idempotency coalescing before records are built.
- write fence check after idempotent duplicates are resolved.
- legacy U32 capacity check before durable append; fail/split tail items that would exceed `maxLegacyMessageSeq`.
- message ID allocation
- encode all new messages
- `group.Append(ctx, records)`
- map base offset to new items
- post-append status recheck

- [ ] **Step 5: Run existing append tests**

Run:

```bash
go test ./pkg/channel/handler -run 'TestAppend' -count=1
```

Expected: PASS.

- [ ] **Step 6: Add idempotency batch tests**

Add tests:

- stored duplicate idempotency returns existing message and bypasses an active write fence.
- two in-batch items with the same idempotency key and same payload return the first item's result.
- two in-batch items with the same idempotency key and different payload fail only the later conflicting item.
- no duplicate idempotency key reaches `store.messageTable.append`.
- legacy U32 boundary fails tail items before durable append.
- post-append status change preserves current single-append behavior.

Run:

```bash
go test ./pkg/channel/handler -run 'TestAppendBatch' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 6: Message SendBatch Durable Path

**Files:**
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/retry.go`
- Modify: `internal/access/node/channel_append_rpc.go`
- Modify: node RPC client/codec files found by `rg "AppendToLeader|channel_append" internal pkg -g'*.go'`
- Test: `internal/usecase/message/send_test.go`
- Test: node RPC tests found near `internal/access/node/*append*test.go`

- [ ] **Step 1: Add batch-capable cluster interface**

In `deps.go`:

```go
type ChannelBatchAppender interface {
    AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}
```

Do the same for remote appender if forwarding needs a batch path:

```go
type RemoteBatchAppender interface {
    AppendBatchToLeader(ctx context.Context, nodeID uint64, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}
```

- [ ] **Step 2: Write failing test for one durable segment using AppendBatch**

Use existing fakes in `send_test.go`; extend fake cluster with `appendBatchCalls`.

Test:

- two same-channel commands.
- `SendBatch` returns two successes.
- fake cluster `AppendBatch` called once with two messages.
- per-item message seq is aligned.

Run:

```bash
go test ./internal/usecase/message -run 'TestSendBatchUsesChannelAppendBatchForSameChannelSegment' -count=1
```

Expected: fail because SendBatch still loops single sends.

- [ ] **Step 3: Implement durable batch segment execution**

In `SendBatch`:

- run the existing per-item preprocessing into a `prepared []preparedSend`.
- skip or fail items whose own context is already canceled.
- collect durable prepared items.
- split into adjacent durable segments.
- derive a segment context that cannot outlive any included item deadline and does not get canceled by one unrelated item.
- for each segment, call batch append if available.
- fallback to single append if batch interface is not available.
- fill `[]SendBatchItemResult` at original indexes.

- [ ] **Step 4: Submit committed dispatcher per successful item**

After batch append success:

- call existing `submitRequestScopedCMDConversationIntent` per item.
- call `dispatcher.SubmitCommitted` per item to preserve existing behavior.
- if dispatcher fails for request-scoped sends, fail that item as current single-send does.

- [ ] **Step 5: Add mixed behavior tests**

Tests:

- one invalid item does not block valid item.
- realtime `NoPersist` item still uses realtime dispatcher.
- different commit mode splits into two batch appends.
- same channel with different epoch expectation splits.
- canceled item does not cancel unrelated item.
- person/agent raw channels that normalize to the same canonical channel preserve order.

- [ ] **Step 6: Add remote batch forwarding path**

Extend the local app cluster wrapper and node RPC layer so follower-ingress sends can forward `AppendBatchRequest` to the leader. If a peer or path does not support batch yet, explicitly de-batch with trace/log visibility rather than silently losing all batching evidence.

Tests:

- follower local message usecase forwards one same-channel segment as one batch RPC.
- stale meta / not leader triggers the same refresh/retry behavior as single send.
- batch RPC returns aligned item results.

Run:

```bash
go test ./internal/usecase/message ./internal/access/node -run 'TestSendBatch|Test.*AppendBatch' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 7: App Config and Wiring

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/build.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write failing config tests**

Add tests:

- defaults are `500us`, `128`, `512KB`.
- env overrides parse.
- invalid negative records/bytes fail validation if explicitly set.

Run:

```bash
go test ./internal/app -run 'TestConfig.*Gateway.*SendBatch|TestLoadConfig.*Gateway.*SendBatch' -count=1
```

Expected: compile/fail because fields do not exist.

- [ ] **Step 2: Add GatewayConfig fields**

In `GatewayConfig`, add English comments:

```go
// SendBatchMaxWait bounds the gateway SEND micro-batch collection delay.
SendBatchMaxWait time.Duration
// SendBatchMaxRecords caps SEND frames collected into one gateway batch.
SendBatchMaxRecords int
// SendBatchMaxBytes caps SEND payload bytes collected into one gateway batch.
SendBatchMaxBytes int
```

Track explicit flags if validation needs to distinguish default from invalid explicit zero.

- [ ] **Step 3: Parse env/config keys**

Add:

```text
WK_GATEWAY_SEND_BATCH_MAX_WAIT
WK_GATEWAY_SEND_BATCH_MAX_RECORDS
WK_GATEWAY_SEND_BATCH_MAX_BYTES
```

Update `wukongim.conf.example` with concise comments.

- [ ] **Step 4: Wire into gateway options**

In `internal/app/build.go`, set gateway session/server options from config.

- [ ] **Step 5: Verify config tests**

Run:

```bash
go test ./internal/app -run 'TestConfig.*Gateway.*SendBatch|TestLoadConfig.*Gateway.*SendBatch' -count=1
```

Expected: PASS.

Checkpoint: do not commit unless requested.

---

## Task 8: Trace and Stress Verification

**Files:**
- Modify: `pkg/observability/sendtrace` files if new stages are needed
- Modify: `internal/app/send_stress_integration_test.go`
- Modify: `internal/app/send_stress_test.go`
- Modify: `internal/app/comm_test.go`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Add trace assertions for batch sizes**

Extend stress trace summaries to log:

- gateway batch records
- channel append batch records
- store commit records
- follower apply records

If existing stages already carry `RecordCount`, prefer reusing them.

- [ ] **Step 2: Add focused unit test for batch trace summary**

Run:

```bash
go test ./internal/app -run 'TestSendStressTrace.*Batch' -count=1
```

Expected first run: fail until summary helper is implemented.

- [ ] **Step 3: Update stress preset if required**

Ensure acceptance preset has enough inflight to form batches:

- keep original `TestSendStressThreeNode` unchanged unless necessary.
- add/keep separate high-channel or hot-channel batch stress tests.
- tune `MaxInflightPerWorker`, `Senders`, and batch config through env/preset.

- [ ] **Step 4: Run focused unit tests**

Run:

```bash
go test ./internal/gateway/core ./internal/access/gateway ./internal/usecase/message ./pkg/channel/handler ./internal/app -run 'Test.*(Batch|Append|SendStress)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run targeted integration tests**

Run:

```bash
WK_SEND_STRESS=1 go test -tags=integration ./internal/app -run '^TestSendStressHighChannelThreeNode$' -count=1 -timeout=300s -v
```

Expected:

- success equals total.
- failed is zero.
- `unique_target_count` equals senders.
- trace shows durable append/store sync `RecordCount > 1`.
- specifically check `replica.leader.durable_append_store` and `store.commit.pebble_sync` stages when trace is enabled.

- [ ] **Step 6: Run original three-node comparison**

Run:

```bash
WK_SEND_STRESS=1 go test -tags=integration ./internal/app -run '^TestSendStressThreeNode$' -count=1 -timeout=300s -v
```

Expected:

- original benchmark still passes.
- QPS should improve if batch config is enabled by acceptance preset.
- if not enabled for original, behavior should remain compatible.

- [ ] **Step 7: Record concise project knowledge**

Add one short bullet to `docs/development/PROJECT_KNOWLEDGE.md` describing the final confirmed batching behavior and any tuning constants.

Checkpoint: do not commit unless requested.

---

## Final Verification

- [ ] Run formatting:

```bash
gofmt -w internal/gateway/types/options.go internal/gateway/core/server.go internal/access/gateway/handler.go internal/access/gateway/frame_router.go internal/usecase/message/command.go internal/usecase/message/deps.go internal/usecase/message/send.go internal/usecase/message/retry.go internal/access/node/channel_append_rpc.go pkg/channel/types.go pkg/channel/handler/append.go internal/app/config.go internal/app/build.go cmd/wukongim/config.go
```

- [ ] Run focused unit suite:

```bash
go test ./internal/gateway/core ./internal/access/gateway ./internal/usecase/message ./pkg/channel/handler ./internal/app -count=1
```

- [ ] Run stress comparison:

```bash
WK_SEND_STRESS=1 go test -tags=integration ./internal/app -run '^TestSendStress(HighChannelThreeNode|ThreeNode)$' -count=1 -timeout=300s -v
```

- [ ] Check diff hygiene:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors, tests pass, no unrelated files modified by the implementation.
