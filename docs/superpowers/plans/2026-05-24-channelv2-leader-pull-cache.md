# Channelv2 Leader Pull Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable per-channel leader suffix cache so recent follower replication pulls can return from memory instead of reading the store every time.

**Architecture:** Each leader `runtimeChannel` owns a small `recentRecordCache` populated only after successful durable leader append. `handlePull` checks this cache before scheduling `TaskStoreReadLog`; cache misses fall back to the existing store-read path, and stale store completions remain fenced by generation, epoch, leader epoch, and role checks.

**Tech Stack:** Go, `pkg/channelv2/reactor`, `pkg/channelv2/service`, existing `worker.Pools`, `store.ChannelStore`, `testify/require` tests.

---

## File Structure

- Create `pkg/channelv2/reactor/record_cache.go`: unexported contiguous suffix cache for cloned `ch.Record` values.
- Create `pkg/channelv2/reactor/record_cache_test.go`: cache-only unit tests.
- Modify `pkg/channelv2/reactor/reactor.go`: add config fields, add cache to `runtimeChannel`, initialize and clear it, populate it on append success, route `handlePull` through cache helpers.
- Modify `pkg/channelv2/reactor/group.go`: expose group-level config defaults and pass cache config into each reactor.
- Modify `pkg/channelv2/service/service.go`: expose facade-level cache config and forward it to `reactor.NewGroup`.
- Modify `pkg/channelv2/reactor/replication_runtime.go`: merge a cache suffix after store prefix reads and reuse one pull-response builder.
- Modify `pkg/channelv2/reactor/replication_state_test.go`: add integration coverage for cache hit, caught-up empty response, DB prefix plus cache suffix, rollover during async read, disabled cache behavior, and metadata fencing.
- Modify `pkg/channelv2/reactor/append_batch_test.go`: update direct caught-up pull tests that no longer schedule a store read.
- Modify `pkg/channelv2/FLOW.md`: mention the leader recent-record cache in the append and pull flow.

## Task 1: Add The Contiguous Recent Record Cache

**Files:**
- Create: `pkg/channelv2/reactor/record_cache.go`
- Create: `pkg/channelv2/reactor/record_cache_test.go`

- [ ] **Step 1: Write failing cache unit tests**

Create `pkg/channelv2/reactor/record_cache_test.go` with:

```go
package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestRecentRecordCacheRetainsNewestContinuousSuffix(t *testing.T) {
	cache := newRecentRecordCache(3, 1024)

	cache.append([]ch.Record{
		{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Index: 2, Payload: []byte("b"), SizeBytes: 1},
		{ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1},
		{ID: 4, Index: 4, Payload: []byte("d"), SizeBytes: 1},
	})

	require.Equal(t, uint64(2), cache.baseOffset)
	require.Equal(t, uint64(4), cache.lastOffset())
	records, ok := cache.slice(2, 4, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{2, 3, 4}, recordIndexes(records))
}

func TestRecentRecordCacheEnforcesByteCap(t *testing.T) {
	cache := newRecentRecordCache(10, 5)

	cache.append([]ch.Record{
		{ID: 1, Index: 1, Payload: []byte("aa"), SizeBytes: 2},
		{ID: 2, Index: 2, Payload: []byte("bbb"), SizeBytes: 3},
		{ID: 3, Index: 3, Payload: []byte("cccc"), SizeBytes: 4},
	})

	require.Equal(t, uint64(3), cache.baseOffset)
	require.Equal(t, 4, cache.bytes)
	records, ok := cache.slice(3, 3, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{3}, recordIndexes(records))
}

func TestRecentRecordCacheSliceHonorsMaxBytesAndClonesPayload(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{
		{ID: 1, Index: 1, Payload: []byte("aa"), SizeBytes: 2},
		{ID: 2, Index: 2, Payload: []byte("bbb"), SizeBytes: 3},
		{ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1},
	})

	records, ok := cache.slice(1, 3, 5)
	require.True(t, ok)
	require.Equal(t, []uint64{1, 2}, recordIndexes(records))
	records[0].Payload[0] = 'z'

	again, ok := cache.slice(1, 1, 1024)
	require.True(t, ok)
	require.Equal(t, []byte("aa"), again[0].Payload)
}

func TestRecentRecordCacheMissesUncoveredOffsets(t *testing.T) {
	cache := newRecentRecordCache(2, 1024)
	cache.append([]ch.Record{
		{ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1},
		{ID: 4, Index: 4, Payload: []byte("d"), SizeBytes: 1},
	})

	_, ok := cache.slice(2, 4, 1024)
	require.False(t, ok)
	_, ok = cache.slice(5, 5, 1024)
	require.False(t, ok)
}

func TestRecentRecordCacheAppendGapResetsSuffix(t *testing.T) {
	cache := newRecentRecordCache(10, 1024)
	cache.append([]ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}})
	cache.append([]ch.Record{{ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1}})

	_, ok := cache.slice(1, 3, 1024)
	require.False(t, ok)
	records, ok := cache.slice(3, 3, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{3}, recordIndexes(records))
}

func TestRecentRecordCacheDisabled(t *testing.T) {
	cache := newRecentRecordCache(0, 1024)
	cache.append([]ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}})

	require.True(t, cache.empty())
	_, ok := cache.slice(1, 1, 1024)
	require.False(t, ok)
}

func recordIndexes(records []ch.Record) []uint64 {
	out := make([]uint64, 0, len(records))
	for _, record := range records {
		out = append(out, record.Index)
	}
	return out
}
```

- [ ] **Step 2: Run cache tests and verify they fail**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestRecentRecordCache' -count=1
```

Expected: FAIL with errors like `undefined: newRecentRecordCache`.

- [ ] **Step 3: Implement the cache**

Create `pkg/channelv2/reactor/record_cache.go` with:

```go
package reactor

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// recentRecordCache keeps a contiguous leader-owned suffix of durable records.
type recentRecordCache struct {
	baseOffset uint64
	records    []ch.Record
	bytes      int
	maxRecords int
	maxBytes   int
}

func newRecentRecordCache(maxRecords int, maxBytes int) recentRecordCache {
	if maxRecords < 0 {
		maxRecords = 0
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	return recentRecordCache{maxRecords: maxRecords, maxBytes: maxBytes}
}

func (c *recentRecordCache) enabled() bool {
	return c != nil && c.maxRecords > 0
}

func (c *recentRecordCache) empty() bool {
	return c == nil || len(c.records) == 0
}

func (c *recentRecordCache) reset() {
	if c == nil {
		return
	}
	c.baseOffset = 0
	c.records = nil
	c.bytes = 0
}

func (c *recentRecordCache) append(records []ch.Record) {
	if !c.enabled() || len(records) == 0 {
		return
	}
	cloned := cloneCacheRecords(records)
	cloned = filterIndexedCacheRecords(cloned)
	if len(cloned) == 0 {
		return
	}
	if c.empty() || cloned[0].Index != c.lastOffset()+1 {
		c.records = cloned
		c.baseOffset = cloned[0].Index
	} else {
		c.records = append(c.records, cloned...)
	}
	c.recountBytes()
	c.trim()
}

func (c *recentRecordCache) lastOffset() uint64 {
	if c.empty() {
		return 0
	}
	return c.baseOffset + uint64(len(c.records)) - 1
}

func (c *recentRecordCache) base() uint64 {
	if c.empty() {
		return 0
	}
	return c.baseOffset
}

func (c *recentRecordCache) hasSuffixAfter(offset uint64) bool {
	return !c.empty() && offset < c.baseOffset && c.baseOffset <= c.lastOffset()
}

func (c *recentRecordCache) covers(offset uint64) bool {
	return !c.empty() && offset >= c.baseOffset && offset <= c.lastOffset()
}

func (c *recentRecordCache) slice(from uint64, maxOffset uint64, maxBytes int) ([]ch.Record, bool) {
	if !c.covers(from) {
		return nil, false
	}
	if maxOffset < from {
		return nil, true
	}
	start := int(from - c.baseOffset)
	endOffset := maxOffset
	if last := c.lastOffset(); endOffset > last {
		endOffset = last
	}
	end := int(endOffset-c.baseOffset) + 1
	out := make([]ch.Record, 0, end-start)
	used := 0
	for _, record := range c.records[start:end] {
		size := cacheRecordSize(record)
		if maxBytes > 0 && used+size > maxBytes && len(out) > 0 {
			break
		}
		if maxBytes > 0 && used+size > maxBytes && len(out) == 0 {
			out = append(out, cloneCacheRecord(record))
			break
		}
		used += size
		out = append(out, cloneCacheRecord(record))
	}
	return out, true
}

func (c *recentRecordCache) trim() {
	if c == nil {
		return
	}
	for c.maxRecords > 0 && len(c.records) > c.maxRecords {
		c.dropFront(1)
	}
	for c.maxBytes > 0 && c.bytes > c.maxBytes && len(c.records) > 1 {
		c.dropFront(1)
	}
	if len(c.records) == 0 {
		c.reset()
	}
}

func (c *recentRecordCache) dropFront(n int) {
	if c == nil || n <= 0 || n > len(c.records) {
		return
	}
	for _, record := range c.records[:n] {
		c.bytes -= cacheRecordSize(record)
	}
	copy(c.records, c.records[n:])
	for i := len(c.records) - n; i < len(c.records); i++ {
		c.records[i] = ch.Record{}
	}
	c.records = c.records[:len(c.records)-n]
	if len(c.records) == 0 {
		c.baseOffset = 0
		c.bytes = 0
		return
	}
	c.baseOffset = c.records[0].Index
}

func (c *recentRecordCache) recountBytes() {
	if c == nil {
		return
	}
	c.bytes = 0
	for _, record := range c.records {
		c.bytes += cacheRecordSize(record)
	}
}

func filterIndexedCacheRecords(records []ch.Record) []ch.Record {
	out := records[:0]
	for _, record := range records {
		if record.Index == 0 {
			continue
		}
		if len(out) > 0 && record.Index != out[len(out)-1].Index+1 {
			out = out[:0]
		}
		out = append(out, record)
	}
	return out
}

func cloneCacheRecords(records []ch.Record) []ch.Record {
	if len(records) == 0 {
		return nil
	}
	out := make([]ch.Record, len(records))
	for i, record := range records {
		out[i] = cloneCacheRecord(record)
	}
	return out
}

func cloneCacheRecord(record ch.Record) ch.Record {
	if len(record.Payload) > 0 {
		payload := make([]byte, len(record.Payload))
		copy(payload, record.Payload)
		record.Payload = payload
	}
	if record.SizeBytes == 0 {
		record.SizeBytes = len(record.Payload)
	}
	return record
}

func cacheRecordSize(record ch.Record) int {
	if record.SizeBytes > 0 {
		return record.SizeBytes
	}
	return len(record.Payload)
}
```

- [ ] **Step 4: Run cache tests and verify they pass**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestRecentRecordCache' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit cache primitive**

Run:

```sh
git add pkg/channelv2/reactor/record_cache.go pkg/channelv2/reactor/record_cache_test.go
git commit -m "feat(channelv2): add leader recent record cache"
```

Expected: commit succeeds and does not include unrelated files such as `pkg/channelv2/FLOW.md` unless this task changed it.

## Task 2: Wire Cache Configuration Through Service And Reactor

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/service/service.go`

- [ ] **Step 1: Write failing config tests**

Add these tests to `pkg/channelv2/reactor/record_cache_test.go`:

```go
func TestDefaultConfigEnablesLeaderRecentRecordCache(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory()})

	require.Equal(t, 10, cfg.LeaderRecentRecordCacheSize)
	require.Equal(t, cfg.PullMaxBytes, cfg.LeaderRecentRecordCacheBytes)
}

func TestDefaultConfigPreservesDisabledLeaderRecentRecordCache(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory(), LeaderRecentRecordCacheSize: -1})

	require.Equal(t, -1, cfg.LeaderRecentRecordCacheSize)
	require.Zero(t, cfg.LeaderRecentRecordCacheBytes)
}
```

Add `store` to the imports in `pkg/channelv2/reactor/record_cache_test.go`:

```go
import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: Run config tests and verify they fail**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestDefaultConfig.*LeaderRecentRecordCache' -count=1
```

Expected: FAIL with errors like `cfg.LeaderRecentRecordCacheSize undefined`.

- [ ] **Step 3: Add config fields and defaults**

In `pkg/channelv2/reactor/group.go`, add fields to `Config` after `PullMaxBytes`:

```go
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls; defaults to 10.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes bounds per-channel memory used by the recent leader log cache.
	LeaderRecentRecordCacheBytes int
```

In `pkg/channelv2/reactor/group.go`, update `NewGroup` to pass the fields into `ReactorConfig` after `PullMaxBytes`:

```go
			LeaderRecentRecordCacheSize:  cfg.LeaderRecentRecordCacheSize,
			LeaderRecentRecordCacheBytes: cfg.LeaderRecentRecordCacheBytes,
```

In `pkg/channelv2/reactor/group.go`, update `defaultConfig` immediately after the `PullMaxBytes` default:

```go
	if cfg.LeaderRecentRecordCacheSize == 0 {
		cfg.LeaderRecentRecordCacheSize = 10
	}
	if cfg.LeaderRecentRecordCacheSize < 0 {
		cfg.LeaderRecentRecordCacheBytes = 0
	} else if cfg.LeaderRecentRecordCacheBytes <= 0 {
		cfg.LeaderRecentRecordCacheBytes = min(cfg.PullMaxBytes, 256*1024)
	}
```

In `pkg/channelv2/reactor/reactor.go`, add fields to `ReactorConfig` after `PullMaxBytes`:

```go
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes bounds per-channel memory used by the recent leader log cache.
	LeaderRecentRecordCacheBytes int
```

In `pkg/channelv2/service/service.go`, add fields to `Config` after `PullMaxBytes`:

```go
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes bounds per-channel memory used by the recent leader log cache.
	LeaderRecentRecordCacheBytes int
```

In `pkg/channelv2/service/service.go`, pass the fields into `reactor.Config` after `PullMaxBytes`:

```go
		LeaderRecentRecordCacheSize:  cfg.LeaderRecentRecordCacheSize,
		LeaderRecentRecordCacheBytes: cfg.LeaderRecentRecordCacheBytes,
```

- [ ] **Step 4: Run config tests and gofmt**

Run:

```sh
gofmt -w pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/group.go pkg/channelv2/service/service.go pkg/channelv2/reactor/record_cache_test.go
go test ./pkg/channelv2/reactor -run 'TestDefaultConfig.*LeaderRecentRecordCache' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit config wiring**

Run:

```sh
git add pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/group.go pkg/channelv2/service/service.go pkg/channelv2/reactor/record_cache_test.go
git commit -m "feat(channelv2): configure leader pull cache"
```

Expected: commit succeeds.

## Task 3: Populate And Invalidate Cache On Leader Runtime State Changes

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/record_cache_test.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write failing population and invalidation tests**

Add this test to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderAppendPopulatesRecentRecordCacheAfterDurableStore(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-populate")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))

	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	rc := r.channels[meta.Key]
	require.Equal(t, uint64(2), rc.recentRecords.base())
	require.Equal(t, uint64(3), rc.recentRecords.lastOffset())
	records, ok := rc.recentRecords.slice(2, 3, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{2, 3}, recordIndexes(records))
}

func TestLeaderRecentRecordCacheClearsOnMetadataFenceAndFollowerRole(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-clear")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.False(t, r.channels[meta.Key].recentRecords.empty())

	updated := meta
	updated.LeaderEpoch = 2
	require.NoError(t, applyMetaDirect(t, r, updated))
	require.True(t, r.channels[meta.Key].recentRecords.empty())

	updated.Leader = 2
	require.NoError(t, applyMetaDirect(t, r, updated))
	require.True(t, r.channels[meta.Key].recentRecords.empty())
}
```

- [ ] **Step 2: Run population tests and verify they fail**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestLeader(AppendPopulatesRecentRecordCache|RecentRecordCacheClears)' -count=1
```

Expected: FAIL with `rc.recentRecords undefined` or assertions showing an empty cache.

- [ ] **Step 3: Add cache to runtimeChannel and initialize it**

In `pkg/channelv2/reactor/reactor.go`, add this field to `runtimeChannel` after `appendInflight`:

```go
	// recentRecords keeps a leader-owned suffix of durable log records for follower pulls.
	recentRecords recentRecordCache
```

In `ensureChannel`, add this field to the `runtimeChannel` literal:

```go
		recentRecords:    newRecentRecordCache(r.cfg.LeaderRecentRecordCacheSize, r.cfg.LeaderRecentRecordCacheBytes),
```

In `handleApplyMeta`, clear the cache when metadata fences state and whenever the local role is not leader. Add these lines inside `if decision.Err == nil`:

```go
		if fencePendingState {
			rc.recentRecords.reset()
		}
```

Then add this line inside the existing `else` branch where `rc.state.Role` is not leader:

```go
			rc.recentRecords.reset()
```

- [ ] **Step 4: Populate cache after successful durable append**

In `handleStoreAppendResult`, inside the existing block:

```go
	if stored.Err == nil && rc.state.Role == ch.RoleLeader && rc.state.LEO > oldLEO {
```

Add this as the first statement in the block:

```go
		rc.recentRecords.append(rc.appendInflightRecords(result.Fence.OpID))
```

Add this helper near `handleStoreAppendResult` in `pkg/channelv2/reactor/reactor.go`:

```go
func (rc *runtimeChannel) appendInflightRecords(batchOpID ch.OpID) []ch.Record {
	if rc == nil || rc.appendInflight == nil || rc.appendInflight.batchOpID != batchOpID {
		return nil
	}
	return rc.appendInflight.records
}
```

This helper reads `appendInflight.records` after `ApplyAppendStored` has assigned offsets through the shared record slice.

- [ ] **Step 5: Run population tests and gofmt**

Run:

```sh
gofmt -w pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_state_test.go
go test ./pkg/channelv2/reactor -run 'TestLeader(AppendPopulatesRecentRecordCache|RecentRecordCacheClears)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit population and invalidation**

Run:

```sh
git add pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "feat(channelv2): populate leader pull cache"
```

Expected: commit succeeds.

## Task 4: Serve Covered And Caught-Up Pulls Without Store Reads

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/reactor/append_batch_test.go`

- [ ] **Step 1: Write failing fast-path tests**

Add these tests to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderPullCoveredByRecentCacheCompletesWithoutStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-hit")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 101,
	})

	result := awaitFutureResult(t, future)
	require.Equal(t, []uint64{1, 2}, recordIndexes(result.Pull.Records))
	require.Equal(t, uint64(2), result.Pull.LeaderLEO)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Zero(t, result.Pull.NextPullAfter)
	require.False(t, r.channels[meta.Key].followers[2].Parked)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullCaughtUpCompletesEmptyWithoutStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-caught-up")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10, LeaderRecentRecordCacheBytes: 1024,
		IdlePullMinInterval: time.Millisecond, IdleEvictAfter: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 2, MaxBytes: 1024,
		},
		OpID: 102,
	})

	result := awaitFutureResult(t, future)
	require.Empty(t, result.Pull.Records)
	require.Equal(t, uint64(1), result.Pull.LeaderLEO)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Greater(t, result.Pull.NextPullAfter, time.Duration(0))
	require.True(t, rc.followers[2].Parked)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullCacheDisabledUsesStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-disabled")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 0, LeaderRecentRecordCacheBytes: 0,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 103,
	})

	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))
	result := awaitFutureResult(t, future)
	require.Equal(t, []uint64{1}, recordIndexes(result.Pull.Records))
}
```

- [ ] **Step 2: Run fast-path tests and verify they fail**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestLeaderPull(CoveredByRecentCache|CaughtUpCompletes|CacheDisabled)' -count=1
```

Expected: FAIL because cache-covered and caught-up pulls still schedule `TaskStoreReadLog`.

- [ ] **Step 3: Extend pull waiter state**

In `pkg/channelv2/reactor/reactor.go`, replace `pullWaiter` with:

```go
type pullWaiter struct {
	// future completes the leader-side pull request once the store read is fenced back.
	future *Future
	// ctx is the caller context used to cancel the waiter before a blocked store read returns.
	ctx context.Context
	// follower is the replica waiting for this pull response.
	follower ch.NodeID
	// nextOffset is the requested offset used to prove caught-up stop eligibility.
	nextOffset uint64
	// maxBytes is the original pull byte budget used for cache suffix merge.
	maxBytes int
	// mergeCacheSuffix allows the store-read result path to append a cache-covered suffix.
	mergeCacheSuffix bool
}
```

- [ ] **Step 4: Add shared pull response helpers**

In `pkg/channelv2/reactor/replication_runtime.go`, add these helpers above `handleStoreReadLogResult`:

```go
func (r *Reactor) completeLeaderPull(rc *runtimeChannel, waiter *pullWaiter, future *Future, records []ch.Record, now time.Time) {
	if rc == nil || rc.state == nil || future == nil {
		return
	}
	version := rc.lifecycle.ActivityVersion
	if version == 0 {
		version = rc.state.LEO
	}
	delay := time.Duration(0)
	control := transport.PullControlContinue
	if len(records) == 0 {
		r.syncLeaderFollowers(rc)
		if waiter != nil && r.leaderCanOfferStop(rc, now) && waiter.nextOffset == rc.state.LEO+1 {
			if follower := rc.followers[waiter.follower]; follower != nil && follower.Match >= rc.state.LEO {
				control = transport.PullControlStop
				follower.StopOffered = true
				follower.StopOfferedVersion = version
			}
		}
		if control != transport.PullControlStop {
			delay = r.leaderPullDelay(rc, now)
		}
	}
	r.updateLeaderPullFollowerState(rc, waiter, len(records), delay, control, now)
	future.Complete(Result{Pull: transport.PullResponse{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		LeaderHW:        rc.state.HW,
		LeaderLEO:       rc.state.LEO,
		ActivityVersion: version,
		NextPullAfter:   delay,
		Control:         control,
		Records:         records,
	}})
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) updateLeaderPullFollowerState(rc *runtimeChannel, waiter *pullWaiter, recordCount int, delay time.Duration, control transport.PullControl, now time.Time) {
	if rc == nil || waiter == nil {
		return
	}
	r.syncLeaderFollowers(rc)
	follower := rc.followers[waiter.follower]
	if follower == nil {
		return
	}
	if recordCount == 0 && control != transport.PullControlStop {
		follower.Parked = delay > 0
		follower.NextExpectedPullAt = now.Add(delay)
		return
	}
	follower.Parked = false
	follower.NextExpectedPullAt = time.Time{}
}
```

- [ ] **Step 5: Add pull cache decision helper**

In `pkg/channelv2/reactor/reactor.go`, add this helper near `handlePull`:

```go
func (r *Reactor) tryCompletePullFromLeaderCache(rc *runtimeChannel, event Event, waiter *pullWaiter, now time.Time) bool {
	if rc == nil || waiter == nil || event.Future == nil || event.Pull.NextOffset > rc.state.LEO+1 {
		return false
	}
	if event.Pull.NextOffset == rc.state.LEO+1 {
		r.completeLeaderPull(rc, waiter, event.Future, nil, now)
		return true
	}
	if records, ok := rc.recentRecords.slice(event.Pull.NextOffset, rc.state.LEO, event.Pull.MaxBytes); ok {
		r.completeLeaderPull(rc, waiter, event.Future, records, now)
		return true
	}
	return false
}

func leaderPullReadRange(rc *runtimeChannel, nextOffset uint64) (maxOffset uint64, mergeCacheSuffix bool) {
	if rc == nil || rc.recentRecords.empty() || !rc.recentRecords.hasSuffixAfter(nextOffset) {
		return rc.state.LEO, false
	}
	base := rc.recentRecords.base()
	if base == 0 || base <= nextOffset {
		return rc.state.LEO, false
	}
	return base - 1, true
}
```

- [ ] **Step 6: Route handlePull through the cache**

In `handlePull`, replace the waiter registration and `submitStoreReadLog` block with:

```go
	waiter := &pullWaiter{future: event.Future, ctx: ctx, follower: event.Pull.Follower, nextOffset: event.Pull.NextOffset, maxBytes: event.Pull.MaxBytes}
	if tryCompleted := r.tryCompletePullFromLeaderCache(rc, event, waiter, now); tryCompleted {
		return
	}
	maxOffset, mergeCacheSuffix := leaderPullReadRange(rc, event.Pull.NextOffset)
	waiter.mergeCacheSuffix = mergeCacheSuffix
	rc.pullWaiters[event.OpID] = waiter
	r.registerPullCancelContext(rc, ctx)
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: event.OpID}
	err = r.submitStoreReadLog(ctx, event.Pull.ChannelID, fence, event.Pull.NextOffset, maxOffset, event.Pull.MaxBytes)
	if err != nil {
		delete(rc.pullWaiters, event.OpID)
		r.unregisterPullCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
```

- [ ] **Step 7: Replace duplicate response construction in store read result path**

In `handleStoreReadLogResult`, replace everything from `records := result.StoreReadLog.Records` through `r.scheduleLifecycleFromState(rc, now)` with:

```go
	records := result.StoreReadLog.Records
	now := time.Now()
	r.completeLeaderPull(rc, waiter, future, records, now)
```

- [ ] **Step 8: Update existing direct caught-up pull tests**

The no-record caught-up pull path now completes immediately, so replace this line in the listed tests:

```go
r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))
```

with:

```go
requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
```

Apply that replacement only in these tests:

```text
pkg/channelv2/reactor/append_batch_test.go: TestAppendAfterZeroVersionStopOfferSendsPullHintImmediately
pkg/channelv2/reactor/replication_state_test.go: TestLeaderPullResponsePacesCaughtUpIdleFollower
pkg/channelv2/reactor/replication_state_test.go: TestLeaderDoesNotOfferStopWhileNonISRFollowerLagging
pkg/channelv2/reactor/replication_state_test.go: TestLeaderReturnsStopWhenAllReplicasCaughtUpAndIdle
pkg/channelv2/reactor/replication_state_test.go: TestLeaderPullResponsePacesFromReplicationIdlePollIntervalByDefault
pkg/channelv2/reactor/replication_state_test.go: TestLeaderPullResponsePacesFromExplicitIdlePullMinInterval
pkg/channelv2/reactor/replication_state_test.go: TestPromotedLeaderUsesLoadedLEOAsActivityVersion
```

Do not change these tests because they still need a store read:

```text
pkg/channelv2/reactor/replication_state_test.go: TestLeaderDoesNotLetFuturePullOffsetFakeStopEligibility
pkg/channelv2/reactor/replication_state_test.go: TestLeaderPullResponsePacesLaggingFollowerWithRecordsImmediately
```

- [ ] **Step 9: Run fast-path tests and gofmt**

Run:

```sh
gofmt -w pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/reactor/append_batch_test.go
go test ./pkg/channelv2/reactor -run 'TestLeaderPull(CoveredByRecentCache|CaughtUpCompletes|CacheDisabled|ResponsePacesCaughtUpIdleFollower|ResponsePacesFromReplicationIdlePollIntervalByDefault|ResponsePacesFromExplicitIdlePullMinInterval|DoesNotOfferStopWhileNonISRFollowerLagging|ReturnsStopWhenAllReplicasCaughtUpAndIdle)|TestAppendAfterZeroVersionStopOfferSendsPullHintImmediately|TestPromotedLeaderUsesLoadedLEOAsActivityVersion' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit pull fast path**

Run:

```sh
git add pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/reactor/append_batch_test.go
git commit -m "feat(channelv2): serve recent leader pulls from cache"
```

Expected: commit succeeds.

## Task 5: Merge Store Prefix With Cache Suffix

**Files:**
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write failing merge tests**

Add these tests to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderPullReadsStorePrefixAndMergesRecentCacheSuffix(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-merge")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 201,
	})

	read := sink.awaitResultKind(t, worker.TaskStoreReadLog)
	r.handleStoreReadLogResult(read)

	result := awaitFutureResult(t, future)
	require.Equal(t, []uint64{1, 2, 3}, recordIndexes(result.Pull.Records))
	require.Zero(t, result.Pull.NextPullAfter)
}

func TestLeaderPullReturnsStorePrefixWhenRecentCacheRollsForwardBeforeMerge(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-rollover")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 202,
	})
	read := sink.awaitResultKind(t, worker.TaskStoreReadLog)

	require.NoError(t, appendDirect(t, r, sink, meta, 4, "d").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 5, "e").Err)
	r.handleStoreReadLogResult(read)

	result := awaitFutureResult(t, future)
	require.Equal(t, []uint64{1}, recordIndexes(result.Pull.Records))
}

func TestLeaderPullMergeHonorsMaxBytes(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-merge-bytes")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "aa").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "bbb").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 5,
		},
		OpID: 203,
	})
	read := sink.awaitResultKind(t, worker.TaskStoreReadLog)
	r.handleStoreReadLogResult(read)

	result := awaitFutureResult(t, future)
	require.Equal(t, []uint64{1, 2}, recordIndexes(result.Pull.Records))
}
```

- [ ] **Step 2: Run merge tests and verify they fail**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestLeaderPull(ReadsStorePrefix|ReturnsStorePrefix|MergeHonors)' -count=1
```

Expected: FAIL because `handleStoreReadLogResult` returns only store records.

- [ ] **Step 3: Implement cache suffix merge**

In `pkg/channelv2/reactor/replication_runtime.go`, add these helpers above `handleStoreReadLogResult`:

```go
func (r *Reactor) mergeLeaderPullCacheSuffix(rc *runtimeChannel, waiter *pullWaiter, records []ch.Record) []ch.Record {
	if rc == nil || waiter == nil || !waiter.mergeCacheSuffix || waiter.maxBytes <= 0 {
		return records
	}
	used := recordsBytes(records)
	if used >= waiter.maxBytes {
		return records
	}
	next := waiter.nextOffset
	if len(records) > 0 {
		next = records[len(records)-1].Index + 1
	}
	if next == 0 || next > rc.state.LEO {
		return records
	}
	suffix, ok := rc.recentRecords.slice(next, rc.state.LEO, waiter.maxBytes-used)
	if !ok || len(suffix) == 0 {
		return records
	}
	merged := make([]ch.Record, 0, len(records)+len(suffix))
	merged = append(merged, records...)
	merged = append(merged, suffix...)
	return merged
}
```

In `handleStoreReadLogResult`, replace:

```go
	records := result.StoreReadLog.Records
```

with:

```go
	records := r.mergeLeaderPullCacheSuffix(rc, waiter, result.StoreReadLog.Records)
```

- [ ] **Step 4: Run merge tests and gofmt**

Run:

```sh
gofmt -w pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/replication_state_test.go
go test ./pkg/channelv2/reactor -run 'TestLeaderPull(ReadsStorePrefix|ReturnsStorePrefix|MergeHonors)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit merge behavior**

Run:

```sh
git add pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "feat(channelv2): merge store prefix with leader cache suffix"
```

Expected: commit succeeds.

## Task 6: Preserve Metadata Fencing, Lifecycle Semantics, And Docs

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/FLOW.md`

- [ ] **Step 1: Write regression tests for fence and stop-control behavior**

Add these tests to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderPullCacheHitDoesNotOfferStopWithRecords(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-no-stop-with-records")
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10, LeaderRecentRecordCacheBytes: 1024,
		IdleEvictAfter: time.Millisecond, IdlePullMinInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 0}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 301,
	})

	result := awaitFutureResult(t, future)
	require.Len(t, result.Pull.Records, 1)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.False(t, rc.followers[2].StopOffered)
}

func TestLeaderPullStorePrefixResultFencedAfterLeaderEpochChange(t *testing.T) {
	factory := newNonCancelingBlockingReadLogFactory()
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, err)
	defer factory.UnblockReadLogs()
	defer g.Close()

	meta := followerTestMeta("recent-cache-fence")
	meta.ISR = []ch.NodeID{1}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	for i, payload := range []string{"a", "b", "c"} {
		future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, uint64(i+1), payload))
		require.NoError(t, err)
		_, err = future.Await(context.Background())
		require.NoError(t, err)
	}

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 302,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	factory.waitReadLogStarted(t)

	updated := meta
	updated.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = pullFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}
```

- [ ] **Step 2: Run regression tests**

Run:

```sh
go test ./pkg/channelv2/reactor -run 'TestLeaderPull(CacheHitDoesNotOfferStop|StorePrefixResultFenced)' -count=1
```

Expected: PASS after Tasks 3-5 are complete.

- [ ] **Step 3: Update FLOW.md**

In `pkg/channelv2/FLOW.md`, update the follower pull section in the Append Sequence from:

```text
Follower->>Workers: TaskRPCPull(leader, local LEO + 1)
Workers->>Reactor: EventPull
Reactor->>Workers: TaskStoreReadLog
Workers-->>Follower: PullResponse(records, leader HW, leader LEO)
```

To:

```text
Follower->>Workers: TaskRPCPull(leader, local LEO + 1)
Workers->>Reactor: EventPull
alt requested offsets covered by leader recent-record cache
    Reactor-->>Follower: PullResponse(records, leader HW, leader LEO)
else cache miss or older prefix needed
    Reactor->>Workers: TaskStoreReadLog
    Workers-->>Reactor: store prefix records
    Reactor-->>Follower: PullResponse(store prefix + optional cache suffix, leader HW, leader LEO)
end
```

Add this paragraph after the Append Sequence diagram:

```markdown
Leader reactors keep a small configurable recent-record suffix cache for durable append records. Follower `Pull` requests that are covered by this suffix can complete from memory; older requests still use `TaskStoreReadLog`, and the leader may append a cache-covered suffix to the store prefix when doing so does not create gaps. The cache is cleared by metadata fences or role changes and is only a performance optimization.
```

- [ ] **Step 4: Run targeted reactor and service tests**

Run:

```sh
gofmt -w pkg/channelv2/reactor/replication_state_test.go
go test ./pkg/channelv2/reactor ./pkg/channelv2/service -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit regression tests and docs**

Run:

```sh
git add pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/FLOW.md
git commit -m "test(channelv2): cover leader pull cache semantics"
```

Expected: commit succeeds. If `pkg/channelv2/FLOW.md` had pre-existing unrelated changes, inspect `git diff -- pkg/channelv2/FLOW.md` and stage only the lines from Step 3 with `git add -p pkg/channelv2/FLOW.md`.

## Task 7: Full Verification

**Files:**
- Verify: `pkg/channelv2/...`

- [ ] **Step 1: Run package-level tests**

Run:

```sh
go test ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Inspect final diff and status**

Run:

```sh
git status --short
git log --oneline -6
```

Expected: working tree contains only intentional uncommitted files, or is clean except for user-owned changes that were present before this work. Recent commits should include the leader pull cache commits from the tasks.

- [ ] **Step 3: Prepare final summary**

Report:

```text
Implemented leader recent-record pull cache for pkg/channelv2.
Verification: go test ./pkg/channelv2/... -count=1 passed.
Noted pre-existing user change: pkg/channelv2/FLOW.md was already modified before this work, so only the cache-flow hunk was staged when committing docs.
```
