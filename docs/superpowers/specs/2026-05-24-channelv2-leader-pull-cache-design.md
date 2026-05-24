# Channelv2 Leader Pull Cache Design

Date: 2026-05-24

## Purpose

Follower replication currently asks the leader to read raw log records from the
channel store for every pull. This is correct, but it makes the hot path pay a
store read even when the follower is only a few records behind the leader. The
goal is to keep a small per-channel leader-side suffix cache, defaulting to the
latest 10 records, so recent follower pulls can be served directly by the
leader reactor.

The cache is a performance optimization only. A miss must fall back to the
existing store-backed `ReadLog` path, and correctness must not depend on cache
contents.

## Scope

In scope:

- Serve follower replication `Pull` requests from a leader-owned recent raw log
  record cache when the requested offset is covered.
- When a follower is behind the cache window, read the missing old prefix from
  the store and optionally append the cache-covered suffix in the same response
  if the byte budget allows.
- Keep existing follower ACK, HW advancement, PullHint, idle delay, and idle
  eviction semantics unchanged.
- Add unit tests around cache behavior and leader pull integration.

Out of scope:

- Client-visible `Fetch` caching.
- Snapshot install, retention repair, or arbitrary sparse log reads.
- Shared cache across reactor instances or process restarts.
- Changing the store contract.

## Recommended Approach

Use a leader reactor local suffix cache owned by each `runtimeChannel`.

The cache sits above `store.ChannelStore` and below the transport API:

```text
leader durable append success
  -> assign offsets in machine.ChannelState
  -> publish assigned records to runtimeChannel.recentRecords
  -> notify followers as today

follower Pull
  -> validate leader/meta/request
  -> try recentRecords
  -> return immediately on covered cache hit
  -> otherwise submit existing TaskStoreReadLog for the missing prefix
  -> merge cache suffix after store result when safe
```

This keeps the optimization in the layer that understands leader role,
metadata fences, follower lifecycle, and pull control. The store remains a
narrow durable persistence contract.

## Data Structures

Add an unexported cache type in `pkg/channelv2/reactor`, for example
`record_cache.go`:

```go
type recentRecordCache struct {
	baseOffset uint64
	records    []ch.Record
	bytes      int
	maxRecords int
	maxBytes   int
}
```

Properties:

- `records` is always a continuous suffix.
- `baseOffset` is the offset of `records[0]`; zero means empty.
- The last cached offset is `baseOffset + uint64(len(records)) - 1`.
- Appending records keeps only the newest `maxRecords` and at most `maxBytes`.
- Records returned from the cache are cloned before leaving the reactor.

The cache should be disabled when `maxRecords <= 0`. A byte cap is included so
that a small record count does not allow unbounded memory from large payloads.

## Configuration

Thread two knobs through `service.Config`, `reactor.Config`, and
`ReactorConfig`:

```go
// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls.
LeaderRecentRecordCacheSize int

// LeaderRecentRecordCacheBytes bounds per-channel memory used by the recent leader log cache.
LeaderRecentRecordCacheBytes int
```

Defaults:

- `LeaderRecentRecordCacheSize = 10`
- `LeaderRecentRecordCacheBytes = min(PullMaxBytes, 256 * 1024)` after
  `PullMaxBytes` has its default applied

Setting the size to zero disables the optimization and preserves the existing
store-read behavior.

## Cache Population

Populate only after leader records are durable:

1. `handleStoreAppendResult` receives a successful store append result.
2. `machine.ChannelState.ApplyAppendStored` validates the fence, assigns
   offsets to the in-flight records, updates `LEO`, and advances local progress.
3. If the runtime is still leader and the stored result has no error, append
   the assigned `batch.records` to `rc.recentRecords`.
4. Continue current append activity, follower state reset, PullHint, and future
   completion flow.

Never populate from queued or in-flight records before the store reports
success. Followers must only receive records the leader has already made
durable locally.

## Pull Fast Path

Update `handlePull` after existing validation and follower visible-state
updates:

1. If `NextOffset == rc.state.LEO + 1`, return the same empty response that the
   store path would produce, including idle delay or `PullControlStop` when
   eligible. No store read is needed.
2. If the recent cache covers `NextOffset`, slice records from the cache up to
   `LEO` and `MaxBytes`, then complete the pull future immediately.
3. If the cache contains a later suffix but not the requested offset, submit a
   store read only for `[NextOffset, cache.baseOffset-1]`. The pull waiter must
   remember the original `MaxBytes` and whether cache suffix merge is allowed.
4. If there is no useful cache suffix, keep the existing store read from
   `NextOffset` to `LEO`.

The immediate response builder should share the existing logic that sets:

- `ChannelKey`
- `Epoch`
- `LeaderEpoch`
- `LeaderHW`
- `LeaderLEO`
- `ActivityVersion`
- `NextPullAfter`
- `Control`
- `Records`

## Store Prefix Merge

Extend `pullWaiter` with enough data to safely merge:

```go
type pullWaiter struct {
	future      *Future
	ctx         context.Context
	follower    ch.NodeID
	nextOffset  uint64
	maxBytes    int
	mergeSuffix bool
}
```

When `handleStoreReadLogResult` receives a successful store result:

1. Validate the existing fence fields and leader role.
2. Start with the store records.
3. If `mergeSuffix` is true and byte budget remains, compute the next offset
   after the store records.
4. Merge cache records only when the cache still covers that exact next offset.
5. If the cache has rolled forward and no longer covers it, return only the
   store records. The follower will pull again.

The merge must never create gaps or reorder records.

## Pull Control and Lifecycle Semantics

Pull control remains tied to whether the response contains records:

- If records are returned from cache, store, or both, return
  `PullControlContinue` with no delay.
- If no records are returned and the follower requested `LEO+1`, reuse the
  existing idle delay and stop-offer logic.
- If no records are returned for another reason, keep `PullControlContinue` and
  allow the follower to pull again according to existing retry behavior.

Follower match updates and ACK-based HW advancement do not change. The leader
may optimistically observe `NextOffset-1` as today, but committed progress still
comes from follower apply plus ACK.

## Fencing and Invalidations

Clear the cache whenever runtime contents may no longer match the current
leader term:

- Runtime creation starts with an empty cache.
- Accepted metadata reload clears the cache.
- Role change away from leader clears the cache.
- Runtime eviction drops the whole `runtimeChannel`, including the cache.
- Stale store read completions keep using existing fence checks and must not
  read or mutate the cache after rejection.

The cache can be rebuilt by future durable leader appends. It does not need to
load old records from store during runtime activation.

## Error Handling

- Cache miss is not an error.
- Cache merge failure due to cache rollover is not an error; return the durable
  store prefix.
- Store read errors continue to complete the pull future with that error.
- Invalid cache configuration disables the cache or falls back to existing
  validation errors; it must not panic.
- Immediate cache responses still honor request context cancellation checked at
  the beginning of `handlePull`.

## Testing

Add focused tests:

- Cache unit tests:
  - Append records and retain only the newest configured count.
  - Enforce byte cap.
  - Slice from a covered offset with `MaxBytes`.
  - Return miss for offsets before `baseOffset`, after the cached suffix, or
    gaps.
- Reactor integration tests:
  - Pull covered by cache completes without `TaskStoreReadLog`.
  - Pull for `LEO+1` completes empty without `TaskStoreReadLog` and preserves
    idle delay or stop control behavior.
  - Pull before cache base reads only the store prefix and merges the suffix
    when byte budget remains.
  - Pull before cache base returns only store records when the cache rolls
    forward before the worker result is applied.
  - Metadata reload or leader epoch change clears cache and stale read results
    are fenced.
  - Setting cache size to zero preserves the current store-read path.

Run targeted tests first:

```sh
go test ./pkg/channelv2/reactor ./pkg/channelv2/service
```

Then run the package-level suite when implementation stabilizes:

```sh
go test ./pkg/channelv2/...
```

## Acceptance Criteria

- Recent follower pulls can be served from memory without scheduling
  `TaskStoreReadLog`.
- Followers that are farther behind can receive a DB prefix plus cache suffix
  without gaps.
- Existing correctness semantics, metadata fencing, lifecycle stop control, and
  ACK-based HW advancement remain unchanged.
- The optimization is configurable and can be disabled.
