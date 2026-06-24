# InternalV2 Message Retention Physical Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Phase 1 physical cleanup for `internalv2` message retention: after a channel's authoritative `RetentionThroughSeq` is committed in slot metadata, each node can asynchronously adopt the boundary and physically delete the local message-log prefix without violating replicated-log consistency.

**Architecture:** Keep `RetentionThroughSeq` as the only authoritative logical boundary in slot metadata. Add bounded retention primitives in `pkg/db/message`, expose them through `pkg/channelv2/store`, apply them through `pkg/channelv2` runtime worker tasks, orchestrate local scans from `pkg/clusterv2`, and wire configuration plus manager safety gates from `internalv2/app` and `internalv2/infra/cluster`.

**Tech Stack:** Go, Pebble-backed `pkg/db/message`, `pkg/channelv2`, `pkg/clusterv2`, `internalv2`, e2e tests with `GOWORK=off /usr/local/go/bin/go`.

---

## Preconditions

- Work from `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM`.
- Read package `FLOW.md` before changing any package covered by this plan.
- Keep single-node deployment semantics as “single-node cluster”.
- Do not implement TTL automatic logical retention in this phase.
- Do not run long integration suites unless explicitly needed.

## Safety Rules

- Never physically delete a seq that is above the authoritative `RetentionThroughSeq`.
- Manager logical advance must not exceed:
  - latest committed visible seq,
  - channel `HW`,
  - current ISR `MinISRMatchOffset`.
- Physical deletion must additionally require local `CheckpointHW`.
- Follower must not compute retention boundaries from local time or local TTL.
- Physical cleanup runs asynchronously and must not run inside the manager HTTP request path.
- `LogStartOffset` remains snapshot-only and is not advanced by retention.
- `LEO` recovery must use `max(messageRowsMaxSeq, RetainedMaxSeq)`.

## Task 1: Add Bounded MessageDB Retention Primitives

Files:

- `pkg/db/message/FLOW.md`
- `pkg/db/message/types.go`
- `pkg/db/message/retention.go`
- `pkg/db/message/compat.go`
- `pkg/db/message/catalog.go`
- `pkg/db/message/retention_test.go`
- `pkg/db/message/catalog_test.go`

Steps:

- [x] Read `pkg/db/message/FLOW.md`.
- [x] Add bounded trim options/result types in `pkg/db/message/types.go`:

```go
type RetentionTrimOptions struct {
    MaxMessages int
    MaxBytes    int
}

type RetentionTrimResult struct {
    DeletedThroughSeq uint64
    Deleted           int
    More              bool
}
```

- [x] Keep existing `RetentionState` fields:
  - `LocalRetentionThroughSeq`
  - `PhysicalRetentionThroughSeq`
  - `RetainedMaxSeq`
- [x] In `pkg/db/message/retention.go`, add a bounded prefix trim helper that:
  - scans rows from `PhysicalRetentionThroughSeq + 1`,
  - stops at `throughSeq`,
  - stops when `MaxMessages` or `MaxBytes` is reached,
  - deletes primary row, payload row, message id index, client msg no index, and `(from_uid, client_msg_no)` index in one batch,
  - updates `PhysicalRetentionThroughSeq`,
  - updates `RetainedMaxSeq` to at least `DeletedThroughSeq`,
  - returns `More=true` when rows remain below `throughSeq`.
- [x] Preserve existing unbounded methods as compatibility wrappers:

```go
func (l *ChannelLog) TrimPrefixThrough(ctx context.Context, throughSeq uint64) (RetentionTrimResult, error)
```

The wrapper should call the bounded implementation with zero limits.

- [x] In `pkg/db/message/compat.go`, add a bounded channel-store entrypoint while preserving existing callers:

```go
func (s *ChannelStore) TrimMessagesThroughLimit(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error)
```

Existing `TrimMessagesThrough(ctx, throughSeq)` remains a wrapper.

- [x] In `pkg/db/message/catalog.go`, add paged catalog scan:

```go
func (db *MessageDB) ListChannelsPage(ctx context.Context, after ChannelKey, limit int) ([]ChannelCatalogEntry, ChannelKey, bool, error)
```

`after` is exclusive. The returned `ChannelKey` is the last key in the page. `bool` is true when another page may exist.

- [x] Ensure catalog scan is ordered by channel key and never loads the full catalog when `limit > 0`.
- [x] Add unit tests:
  - `TestChannelRetentionTrimPrefixBoundedDeletesRowsAndIndexes`
  - `TestChannelRetentionTrimPrefixPreservesLEOAfterAllRowsDeleted`
  - `TestChannelRetentionTrimPrefixRepeatedCallNoop`
  - `TestMessageDBListChannelsPage`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/message -run 'Retention|Catalog' -count=1
```

## Task 2: Expose Retention Through ChannelV2 Store

Files:

- `pkg/channelv2/FLOW.md`
- `pkg/channelv2/store/adapter.go`
- `pkg/channelv2/store/channel_adapter.go`
- `pkg/channelv2/store/memory.go`
- `pkg/channelv2/store/channel_adapter_test.go`
- `pkg/channelv2/store/memory_test.go`

Steps:

- [x] Read `pkg/channelv2/FLOW.md`.
- [x] In `pkg/channelv2/store/adapter.go`, add store-facing retention types:

```go
type RetentionState struct {
    LocalRetentionThroughSeq    uint64
    PhysicalRetentionThroughSeq uint64
    RetainedMaxSeq              uint64
}

type RetentionTrimOptions struct {
    MaxMessages int
    MaxBytes    int
}

type RetentionTrimResult struct {
    DeletedThroughSeq uint64
    Deleted           int
    More              bool
}
```

- [x] Extend `ChannelStore`:

```go
LoadRetentionState(ctx context.Context) (RetentionState, error)
AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error)
TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error)
```

- [x] Update `pkg/channelv2/store/channel_adapter.go` to map message DB retention types to channelv2 store types.
- [x] Update `pkg/channelv2/store/memory.go` with equivalent in-memory retention state:
  - adoption is monotonic,
  - trim deletes in-memory records up to the bounded limit,
  - LEO remains at least `RetainedMaxSeq`.
- [x] Add tests:
  - `TestChannelStoreAdapterRetentionAdoptAndTrim`
  - `TestMemoryStoreRetentionAdoptAndTrim`
  - `TestMemoryStoreRetentionLEOAfterTrim`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/channelv2/store -run 'Retention' -count=1
```

## Task 3: Add ChannelV2 Retention View And Apply Runtime

Files:

- `pkg/channelv2/FLOW.md`
- `pkg/channelv2/worker/FLOW.md`
- `pkg/channelv2/reactor/FLOW.md`
- `pkg/channelv2/retention.go`
- `pkg/channelv2/machine/channel.go`
- `pkg/channelv2/machine/meta.go`
- `pkg/channelv2/reactor/event.go`
- `pkg/channelv2/reactor/group.go`
- `pkg/channelv2/reactor/lifecycle_controller.go`
- `pkg/channelv2/reactor/runtime_snapshot.go`
- `pkg/channelv2/reactor/retention.go`
- `pkg/channelv2/worker/task.go`
- `pkg/channelv2/worker/pools.go`
- `pkg/channelv2/service/retention.go`
- `pkg/channelv2/reactor/retention_test.go`
- `pkg/channelv2/service/retention_test.go`

Steps:

- [x] Read `pkg/channelv2/FLOW.md`, `pkg/channelv2/worker/FLOW.md`, and `pkg/channelv2/reactor/FLOW.md`.
- [x] Create `pkg/channelv2/retention.go`:

```go
type RetentionView struct {
    Key                         ChannelID
    Role                        Role
    Leader                      uint64
    Replicas                    []uint64
    ISR                         []uint64
    RetentionThroughSeq         uint64
    LocalRetentionThroughSeq    uint64
    PhysicalRetentionThroughSeq uint64
    LEO                         uint64
    HW                          uint64
    CheckpointHW                uint64
    MinISRMatchOffset           uint64
}

type RetentionApplyOptions struct {
    MaxTrimMessages int
    MaxTrimBytes    int
}

type RetentionApplyRequest struct {
    Key        ChannelID
    ThroughSeq uint64
    Options    RetentionApplyOptions
}

type RetentionApplyResult struct {
    Key                         ChannelID
    ThroughSeq                  uint64
    LocalRetentionThroughSeq    uint64
    PhysicalRetentionThroughSeq uint64
    DeletedThroughSeq           uint64
    Deleted                     int
    More                        bool
    BlockedReason               string
}
```

- [x] Add English comments for exported types and fields.
- [x] Add retention fields to `machine.ChannelState`:
  - `RetentionThroughSeq`
  - `LocalRetentionThroughSeq`
  - `PhysicalRetentionThroughSeq`
- [x] In `machine/meta.go`, apply `Meta.RetentionThroughSeq` monotonically into channel state. Never decrease it.
- [x] When loading a channel runtime, initialize local retention state by calling `ChannelStore.LoadRetentionState`.
- [x] Extend runtime snapshot/view code to compute `MinISRMatchOffset` from current ISR follower progress. Unknown ISR member progress must cap the value at current `RetentionThroughSeq`.
- [x] Add reactor events:
  - `EventRetentionView`
  - `EventApplyRetentionBoundary`
  - `EventRetentionApplied`
- [x] Add worker task for store adoption and trim:

```go
const TaskStoreRetention TaskType = ...
```

Route it to the existing store worker pool.

- [x] Implement `pkg/channelv2/reactor/retention.go`:
  - `RetentionView(key ChannelID) (RetentionView, error)`,
  - `ApplyRetentionBoundary(req RetentionApplyRequest) (RetentionApplyResult, error)`,
  - reactor publishes `RetentionThroughSeq` immediately when request is monotonic,
  - store worker performs `AdoptRetentionBoundary`,
  - store worker only trims when local conditions are satisfied.
- [x] Local trim conditions:
  - `ThroughSeq <= HW`,
  - `ThroughSeq <= CheckpointHW`,
  - `ThroughSeq <= LEO`,
  - `ThroughSeq > PhysicalRetentionThroughSeq`.
- [x] Leader trim additionally requires `ThroughSeq <= MinISRMatchOffset`.
- [x] If local conditions fail, return `BlockedReason` and keep adoption result.
- [x] Add `pkg/channelv2/service/retention.go` to expose retention view/apply through the public service used by `pkg/clusterv2`.
- [x] Add tests:
  - `TestRetentionViewReportsRuntimeAndStoreState`
  - `TestApplyRetentionBoundaryPublishesLogicalFloorBeforeTrim`
  - `TestApplyRetentionBoundaryBlocksTrimWhenCheckpointLags`
  - `TestLeaderRetentionTrimBlocksWhenMinISRLags`
  - `TestApplyRetentionBoundaryUpdatesPhysicalProgressAfterWorkerResult`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/channelv2/reactor ./pkg/channelv2/service -run 'Retention' -count=1
```

## Task 4: Add ClusterV2 Physical GC Orchestration

Files:

- `pkg/clusterv2/FLOW.md`
- `pkg/clusterv2/config.go`
- `pkg/clusterv2/node.go`
- `pkg/clusterv2/channel_retention_physical.go`
- `pkg/clusterv2/channel_retention_physical_test.go`

Steps:

- [x] Read `pkg/clusterv2/FLOW.md`.
- [x] Add config:

```go
type ChannelRetentionConfig struct {
    PhysicalGCEnabled  bool
    ScanInterval       time.Duration
    ChannelBatchSize   int
    MaxTrimMessages    int
    MaxTrimBytes       int
}
```

- [x] Default values:
  - `PhysicalGCEnabled=false`,
  - `ScanInterval=1 * time.Minute`,
  - `ChannelBatchSize=128`,
  - `MaxTrimMessages=1000`,
  - `MaxTrimBytes=0`.
- [x] Add node methods:

```go
func (n *Node) ChannelRetentionView(ctx context.Context, key channelv2.ChannelID) (channelv2.RetentionView, error)
func (n *Node) ApplyChannelRetentionBoundary(ctx context.Context, key channelv2.ChannelID, throughSeq uint64, opts channelv2.RetentionApplyOptions) (channelv2.RetentionApplyResult, error)
func (n *Node) RunChannelRetentionGCOnce(ctx context.Context) (ChannelRetentionGCResult, error)
```

- [x] Define result structs with English comments:

```go
type ChannelRetentionGCResult struct {
    ScannedChannels int
    AppliedChannels int
    TrimmedChannels int
    DeletedMessages int
    BlockedChannels int
    Errors          int
}
```

- [x] `RunChannelRetentionGCOnce` must:
  - scan local message catalog through `MessageDB.ListChannelsPage`,
  - read authoritative channel runtime metadata from slot metadata,
  - skip channels with `RetentionThroughSeq == 0`,
  - call `ApplyChannelRetentionBoundary` with bounded trim options,
  - continue after per-channel errors and record counts.
- [x] Add a lifecycle loop in `Node.Start` only when `PhysicalGCEnabled` is true.
- [x] The lifecycle loop should stop on node context cancellation.
- [x] Add tests:
  - `TestChannelRetentionGCOnceSkipsChannelsWithoutBoundary`
  - `TestChannelRetentionGCOnceAppliesCommittedBoundary`
  - `TestChannelRetentionGCOnceContinuesAfterChannelError`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/clusterv2 -run 'ChannelRetention' -count=1
```

## Task 5: Tighten Manager Logical Boundary Safety Gates

Files:

- `internalv2/infra/cluster/FLOW.md`
- `internalv2/infra/cluster/management_message_retention.go`
- `internalv2/usecase/management/message_retention.go`
- `internalv2/infra/cluster/management_message_retention_test.go`

Steps:

- [x] Read `internalv2/infra/cluster/FLOW.md`.
- [x] Extend the infra dependency interface to read channelv2 retention safety view from clusterv2.
- [x] In `management_message_retention.go`, compute:

```go
safeThroughSeq := minNonZero(
    requestedThroughSeq,
    latestCommittedVisibleSeq,
    view.HW,
    view.MinISRMatchOffset,
)
```

- [x] If `safeThroughSeq < requestedThroughSeq`, return or record the most specific blocked reason:
  - `hw_lag`,
  - `min_isr_match_offset`,
  - `no_committed_message`.
- [x] Keep non-leader forwarding behavior exactly once.
- [x] Do not use replay cursor as a Phase 1 gate because internalv2 does not yet have a durable committed replay cursor contract.
- [x] Add tests:
  - `TestMessageRetentionBlocksWhenHWBelowRequested`
  - `TestMessageRetentionBlocksWhenCheckpointBelowRequested`
  - `TestMessageRetentionBlocksWhenMinISRBelowRequested`
  - `TestMessageRetentionAllowsSafeRequestedBoundary`
  - `TestMessageRetentionForwardedLeaderUsesFreshRetentionView`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/infra/cluster ./internalv2/usecase/management -run 'Retention' -count=1
```

## Task 6: Wire InternalV2 Config And App Lifecycle

Files:

- `internalv2/app/FLOW.md`
- `internalv2/app/config.go`
- `internalv2/app/app.go`
- `internalv2/app/wire.go`
- `cmd/wukongimv2/config.go`
- `cmd/wukongimv2/wukongimv2.conf.example`
- `scripts/wukongimv2/*.conf`
- `internalv2/app/config_test.go`

Steps:

- [x] Read `internalv2/app/FLOW.md`.
- [x] Add config struct:

```go
type ChannelMessageRetentionConfig struct {
    PhysicalGCEnabled bool
    ScanInterval      time.Duration
    ChannelBatchSize  int
    MaxTrimMessages   int
    MaxTrimBytes      int
}
```

- [x] Add English comments for all fields.
- [x] Parse config keys in `cmd/wukongimv2/config.go`:
  - `WK_CHANNEL_MESSAGE_RETENTION_PHYSICAL_GC_ENABLE`
  - `WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL`
  - `WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE`
  - `WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES`
  - `WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_BYTES`
- [x] Update `cmd/wukongimv2/wukongimv2.conf.example`.
- [x] Update v2 script config files only when they need explicit non-default values for tests.
- [x] Pass config into `pkg/clusterv2.ChannelRetentionConfig`.
- [x] Ensure default background physical GC is disabled.
- [x] Add config tests:
  - `TestChannelMessageRetentionConfigDefaults`
  - `TestChannelMessageRetentionConfigEnvOverrides`

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/app ./cmd/wukongimv2 -run 'Retention|Config' -count=1
```

## Task 7: Add E2E And Integration Coverage

Files:

- `test/e2ev2/message/message_retention/message_retention_test.go`
- `test/e2ev2/suite/*`
- `cmd/wkdb/*` only if the test needs a supported local DB inspection helper

Steps:

- [x] Extend existing message retention e2e so the three-node topology can enable physical GC with bounded limits.
- [x] Send enough messages to create a prefix and a suffix.
- [x] Advance retention through a prefix seq through manager API from a non-channel-leader node.
- [x] Assert all nodes hide retained seqs through normal message read APIs.
- [x] Wait for physical GC to run or call a test-only GC trigger through existing in-process harness if available.
- [x] Verify retained seq rows and secondary index lookups are absent from local message DB. Prefer supported store APIs or `wkdb` helper over raw Pebble access.
- [x] Restart the channel leader.
- [x] Assert:
  - logical boundary remains,
  - retained seqs do not reappear,
  - new messages append after the retained prefix,
  - suffix messages remain readable.

Verification:

```bash
GOWORK=off /usr/local/go/bin/go test -tags=e2e ./test/e2ev2/message/message_retention -count=1 -timeout 2m -p=1
```

## Task 8: Update FLOW And Design Docs

Files:

- `pkg/db/message/FLOW.md`
- `pkg/channelv2/FLOW.md`
- `pkg/channelv2/worker/FLOW.md`
- `pkg/channelv2/reactor/FLOW.md`
- `pkg/clusterv2/FLOW.md`
- `internalv2/infra/cluster/FLOW.md`
- `internalv2/app/FLOW.md`
- `docs/superpowers/specs/2026-06-23-internalv2-message-retention-physical-cleanup-design.md`

Steps:

- [x] Document MessageDB retention state, bounded trim, and paged catalog scan.
- [x] Document ChannelV2 retention view/apply events and worker task.
- [x] Document ClusterV2 physical GC loop and disabled-by-default config.
- [x] Document manager safety gates and why replay cursor is not a Phase 1 gate.
- [x] Ensure docs consistently say “single-node cluster”.

Verification:

```bash
git diff --check -- pkg/db/message/FLOW.md pkg/channelv2/FLOW.md pkg/channelv2/worker/FLOW.md pkg/channelv2/reactor/FLOW.md pkg/clusterv2/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/app/FLOW.md docs/superpowers/specs/2026-06-23-internalv2-message-retention-physical-cleanup-design.md
```

## Task 9: Final Focused Verification

Run focused package tests:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/message ./pkg/channelv2/store ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/channelv2/worker ./pkg/clusterv2 ./internalv2/infra/cluster ./internalv2/usecase/management ./internalv2/app ./cmd/wukongimv2 -run 'Retention|ChannelRetention|Config|Checkpoint' -count=1
```

Run the e2e retention suite:

```bash
GOWORK=off /usr/local/go/bin/go test -tags=e2e ./test/e2ev2/message/message_retention -count=1 -timeout 2m -p=1
```

Run a final diff check:

```bash
git diff --check
```

Completion criteria:

- Manual manager retention still advances logical boundary through the channel leader.
- Logical reads hide retained seqs before physical deletion completes.
- Physical GC deletes retained local rows and indexes asynchronously.
- Leader and follower never trim above `MinISRMatchOffset`.
- Restart does not make retained seqs visible again.
- `LEO` does not move backward after an entire prefix is deleted.
- Background physical GC is disabled by default and configurable for internalv2.
