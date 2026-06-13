# channelappend conservative coalescing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move channelappend prepare work out of `channelWriter.mu`, add a tiny writer-side inbox coalescing window, and keep benchmarks/documentation aligned with the new batching behavior.

**Architecture:** `SubmitLocal` remains non-blocking after local admission. The scheduled writer goroutine briefly waits outside `channelWriter.mu` to collect nearby same-channel inbox work, then takes the inbox, prepares outside the lock, and re-locks only to admit prepared results into `channelState`. Runtime-only `Options` fields control the window and max coalesced logical items; external config is unchanged in this pass.

**Tech Stack:** Go, `internalv2/runtime/channelappend`, Go unit tests, Go race tests, package benchmarks, existing worker pool and writer state machine.

---

## Execution Setup

Run implementation in an isolated worktree because this change touches hot runtime code and must merge back to local `main` only after verification.

```bash
git status --short
git worktree add ../WuKongIM-channelappend-coalescing-fix -b codex/channelappend-coalescing-fix main
cd ../WuKongIM-channelappend-coalescing-fix
sed -n '1,220p' internalv2/runtime/channelappend/FLOW.md
sed -n '1,220p' docs/development/PERF_TRIAGE.md
```

Expected:
- `git status --short` may show unrelated untracked files in the original checkout; do not stage or remove them.
- New branch is `codex/channelappend-coalescing-fix`.
- `FLOW.md` and `PERF_TRIAGE.md` have been read before code changes.

## File Structure

- Modify `internalv2/runtime/channelappend/options.go`
  - Add runtime-only coalescing defaults and `Options` fields.
  - Apply default/disable semantics in `applyDefaults`.
- Modify `internalv2/runtime/channelappend/options_test.go`
  - Lock default and explicit-disable option behavior.
- Modify `internalv2/runtime/channelappend/writer.go`
  - Add writer ports for coalescing.
  - Split inbox drain into take/prepare/admit phases.
  - Add bounded wait before taking a small inbox.
- Modify `internalv2/runtime/channelappend/group.go`
  - Pass coalescing options from `Options` into each shard writer port.
- Modify `internalv2/runtime/channelappend/writer_testhelpers_test.go`
  - Let writer-level tests inject `MessageIDAllocator` and coalescing knobs.
- Modify `internalv2/runtime/channelappend/writer_advance_test.go`
  - Add prepare-outside-lock and inbox coalescing regression tests.
- Modify `internalv2/runtime/channelappend/benchmark_test.go`
  - Keep old submit/wait microbenchmarks measuring non-coalesced hot path with explicit disable.
  - Add a burst benchmark that exercises coalescing.
- Modify `internalv2/runtime/channelappend/FLOW.md`
  - Document the writer-side wait and two-phase prepare/admit flow.

## Task 1: Runtime Option Semantics

**Files:**
- Modify: `internalv2/runtime/channelappend/options_test.go`
- Modify: `internalv2/runtime/channelappend/options.go`

- [ ] **Step 1: Write the failing option tests**

Replace `internalv2/runtime/channelappend/options_test.go` with:

```go
package channelappend

import (
	"testing"
	"time"
)

func TestDefaultAppendInflightBatchesPerChannelIsTen(t *testing.T) {
	opts := applyDefaults(Options{})

	if opts.AppendInflightBatchesPerChannel != 10 {
		t.Fatalf("AppendInflightBatchesPerChannel = %d, want 10", opts.AppendInflightBatchesPerChannel)
	}
}

func TestDefaultInboxCoalesceOptionsAreConservative(t *testing.T) {
	opts := applyDefaults(Options{})

	if opts.InboxCoalesceWindow != 250*time.Microsecond {
		t.Fatalf("InboxCoalesceWindow = %s, want 250µs", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 16 {
		t.Fatalf("InboxCoalesceMaxItems = %d, want 16", opts.InboxCoalesceMaxItems)
	}
}

func TestNegativeInboxCoalesceOptionsDisableCoalescing(t *testing.T) {
	opts := applyDefaults(Options{InboxCoalesceWindow: -time.Nanosecond})

	if opts.InboxCoalesceWindow != 0 {
		t.Fatalf("InboxCoalesceWindow = %s, want disabled zero", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 0 {
		t.Fatalf("InboxCoalesceMaxItems = %d, want disabled zero", opts.InboxCoalesceMaxItems)
	}

	opts = applyDefaults(Options{InboxCoalesceMaxItems: -1})
	if opts.InboxCoalesceWindow != 0 {
		t.Fatalf("InboxCoalesceWindow with negative max = %s, want disabled zero", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 0 {
		t.Fatalf("InboxCoalesceMaxItems with negative max = %d, want disabled zero", opts.InboxCoalesceMaxItems)
	}
}
```

- [ ] **Step 2: Run the option tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestDefault.*Coalesce|TestNegativeInboxCoalesce' -count=1
```

Expected: FAIL at compile time with `opts.InboxCoalesceWindow undefined` and `unknown field InboxCoalesceWindow`.

- [ ] **Step 3: Add option fields and defaults**

In `internalv2/runtime/channelappend/options.go`, add constants next to the existing channel append defaults:

```go
	defaultInboxCoalesceWindow                  = 250 * time.Microsecond
	defaultInboxCoalesceMaxItems                = 16
```

In the `Options` struct, add these fields immediately after `AppendInflightBatchesPerChannel`:

```go
	// InboxCoalesceWindow is the bounded delay a scheduled writer may wait to merge near-simultaneous same-channel submissions. A negative value disables coalescing; zero uses the runtime default.
	InboxCoalesceWindow time.Duration
	// InboxCoalesceMaxItems bounds logical send items collected during one coalescing wait. A negative value disables coalescing; zero uses the runtime default.
	InboxCoalesceMaxItems int
```

In `applyDefaults`, add this block immediately after the `AppendInflightBatchesPerChannel` default:

```go
	if opts.InboxCoalesceWindow < 0 || opts.InboxCoalesceMaxItems < 0 {
		opts.InboxCoalesceWindow = 0
		opts.InboxCoalesceMaxItems = 0
	} else {
		if opts.InboxCoalesceWindow == 0 {
			opts.InboxCoalesceWindow = defaultInboxCoalesceWindow
		}
		if opts.InboxCoalesceMaxItems == 0 {
			opts.InboxCoalesceMaxItems = defaultInboxCoalesceMaxItems
		}
	}
```

- [ ] **Step 4: Verify option tests pass**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/options.go internalv2/runtime/channelappend/options_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestDefault.*Coalesce|TestNegativeInboxCoalesce|TestDefaultAppendInflightBatchesPerChannelIsTen' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit option semantics**

Run:

```bash
git add internalv2/runtime/channelappend/options.go internalv2/runtime/channelappend/options_test.go
git commit -m "perf(channelappend): add inbox coalescing runtime options"
```

Expected: commit succeeds.

## Task 2: Prepare Outside The Writer Lock

**Files:**
- Modify: `internalv2/runtime/channelappend/writer_testhelpers_test.go`
- Modify: `internalv2/runtime/channelappend/writer_advance_test.go`
- Modify: `internalv2/runtime/channelappend/writer.go`

- [ ] **Step 1: Extend writer test helpers**

In `internalv2/runtime/channelappend/writer_testhelpers_test.go`, replace `writerRuntimeConfig` and the relevant part of `newWriterRuntime` with:

```go
type writerRuntimeConfig struct {
	appender              Appender
	messageID             MessageIDAllocator
	inboxCoalesceWindow   time.Duration
	inboxCoalesceMaxItems int
}
```

```go
func newWriterRuntime(t *testing.T, cfg writerRuntimeConfig) *writerRuntime {
	t.Helper()
	limits := channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1}
	target := benchmarkAuthorityTarget("rt")
	w := newChannelWriter(target, limits)
	messageID := cfg.messageID
	if messageID == nil {
		messageID = newBenchmarkMessageIDs(1)
	}
	rt := &writerRuntime{t: t, pool: newWorkerPool(4), writer: w}
	w.ports = writerPorts{
		prepare:               preparePorts{messageID: messageID},
		append:                appendPorts{appender: cfg.appender},
		commit:                commitPorts{},
		pool:                  rt.pool,
		schedule:              rt.schedule,
		runtimeCtx:            context.Background(),
		inboxCoalesceWindow:   cfg.inboxCoalesceWindow,
		inboxCoalesceMaxItems: cfg.inboxCoalesceMaxItems,
	}
	return rt
}
```

Ensure the file imports `time`:

```go
import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)
```

- [ ] **Step 2: Add the prepare-outside-lock regression test**

In `internalv2/runtime/channelappend/writer_advance_test.go`, change imports to:

```go
import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)
```

Add this test and helper type after `TestWriterAdvanceCompletesWithinDeadline`:

```go
func TestWriterAdvancePreparesOutsideWriterLock(t *testing.T) {
	ids := newBlockingMessageIDsForWriterTest()
	rt := newWriterRuntime(t, writerRuntimeConfig{
		appender:  &orderedAppender{},
		messageID: ids,
	})
	defer rt.stop()

	target := benchmarkAuthorityTarget("prepare-outside-lock")
	first, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("prepare-outside-lock")})
	if err != nil {
		t.Fatalf("first submit error = %v", err)
	}
	select {
	case <-ids.started:
	case <-time.After(time.Second):
		t.Fatalf("prepare did not reach MessageID allocation")
	}

	secondDone := make(chan struct{})
	var second *Future
	var secondErr error
	go func() {
		defer close(secondDone)
		second, secondErr = rt.submit(target, []SendBatchItem{benchmarkSendItem("prepare-outside-lock")})
	}()

	select {
	case <-secondDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("second submit blocked while the writer was preparing the first inbox batch")
	}
	if secondErr != nil {
		t.Fatalf("second submit error = %v", secondErr)
	}
	if second == nil {
		t.Fatalf("second future is nil")
	}

	close(ids.release)
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatalf("first wait error = %v", err)
	}
	if _, err := second.Wait(context.Background()); err != nil {
		t.Fatalf("second wait error = %v", err)
	}
}

type blockingMessageIDsForWriterTest struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	next    atomic.Uint64
}

func newBlockingMessageIDsForWriterTest() *blockingMessageIDsForWriterTest {
	return &blockingMessageIDsForWriterTest{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (ids *blockingMessageIDsForWriterTest) Next() uint64 {
	ids.once.Do(func() {
		close(ids.started)
		<-ids.release
	})
	return ids.next.Add(1)
}
```

- [ ] **Step 3: Run the prepare lock regression test to verify it fails**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/writer_testhelpers_test.go internalv2/runtime/channelappend/writer_advance_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run TestWriterAdvancePreparesOutsideWriterLock -count=1
```

Expected: FAIL after about `100ms` with `second submit blocked while the writer was preparing the first inbox batch`.

- [ ] **Step 4: Split inbox drain into take, prepare, and admit phases**

In `internalv2/runtime/channelappend/writer.go`, add this helper type near `submittedBatch`:

```go
// preparedInboxBatch pairs an accepted submitted batch with its lock-free prepare outcome.
type preparedInboxBatch struct {
	batch   submittedBatch
	outcome prepareOutcome
}
```

Replace both `advance` loops with:

```go
func (w *channelWriter) advance() {
	if !w.ports.commit.hasPostCommitWork() {
		w.advanceAppendOnly()
		return
	}
	var appendEff appendEffect
	var commitEff commitEffect
	for {
		w.mu.Lock()
		inbox := w.takeInboxLocked()
		if len(inbox) > 0 {
			w.mu.Unlock()
			prepared := w.prepareInbox(inbox)
			w.mu.Lock()
			w.admitPreparedInboxLocked(prepared)
		}
		hasAppend := w.nextAppendLocked(&appendEff)
		hasCommit := w.nextCommitLocked(&commitEff)
		if !hasAppend && !hasCommit {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
		}
		if hasCommit {
			w.runCommit(&commitEff)
		}
	}
}

func (w *channelWriter) advanceAppendOnly() {
	var appendEff appendEffect
	for {
		w.mu.Lock()
		inbox := w.takeInboxLocked()
		if len(inbox) > 0 {
			w.mu.Unlock()
			prepared := w.prepareInbox(inbox)
			w.mu.Lock()
			w.admitPreparedInboxLocked(prepared)
		}
		hasAppend := w.nextAppendLocked(&appendEff)
		if !hasAppend {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		w.runAppend(&appendEff)
	}
}
```

Replace `drainInboxLocked` with these helpers:

```go
// takeInboxLocked detaches all currently accepted inbox batches from the writer.
func (w *channelWriter) takeInboxLocked() []submittedBatch {
	if len(w.inbox) == 0 {
		return nil
	}
	inbox := w.inbox
	w.inbox = nil
	return inbox
}

// prepareInbox runs CPU-side send preparation outside channelWriter.mu.
func (w *channelWriter) prepareInbox(inbox []submittedBatch) []preparedInboxBatch {
	if len(inbox) == 0 {
		return nil
	}
	prepared := make([]preparedInboxBatch, 0, len(inbox))
	for _, batch := range inbox {
		prepared = append(prepared, preparedInboxBatch{
			batch:   batch,
			outcome: prepareBatch(w.ports.runtimeCtx, batch.items, w.ports.prepare),
		})
	}
	return prepared
}

// admitPreparedInboxLocked moves prepared work into channelState while w.mu is held.
func (w *channelWriter) admitPreparedInboxLocked(prepared []preparedInboxBatch) {
	for _, item := range prepared {
		w.admitPreparedLocked(item.batch, item.outcome)
	}
}
```

- [ ] **Step 5: Verify prepare-outside-lock and existing advance tests pass**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/writer_testhelpers_test.go internalv2/runtime/channelappend/writer_advance_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestWriterAdvance|TestAdvancePostCommitReusesEffectWithoutBleed' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit prepare lock reduction**

Run:

```bash
git add internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/writer_testhelpers_test.go internalv2/runtime/channelappend/writer_advance_test.go
git commit -m "perf(channelappend): prepare inbox outside writer lock"
```

Expected: commit succeeds.

## Task 3: Conservative Inbox Coalescing

**Files:**
- Modify: `internalv2/runtime/channelappend/writer.go`
- Modify: `internalv2/runtime/channelappend/group.go`
- Modify: `internalv2/runtime/channelappend/writer_advance_test.go`

- [ ] **Step 1: Add coalescing behavior tests**

In `internalv2/runtime/channelappend/writer_advance_test.go`, add `errors` to the imports:

```go
import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)
```

Add these tests and helper type before `TestAdvancePostCommitReusesEffectWithoutBleed`:

```go
func TestInboxCoalesceMergesSingleItemSubmissions(t *testing.T) {
	appender := &recordingBatchAppenderForCoalesceTest{}
	group := New(Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		Appender:                   appender,
		MessageID:                  newBenchmarkMessageIDs(1),
		InboxCoalesceWindow:        20 * time.Millisecond,
		InboxCoalesceMaxItems:      8,
		ConversationActiveAdmitter: nil,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	target := benchmarkAuthorityTarget("coalesce-merge")
	const submissions = 5
	futures := make([]*Future, submissions)
	for i := 0; i < submissions; i++ {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("coalesce-merge")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = future
	}
	for i, future := range futures {
		results, err := future.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			t.Fatalf("result %d = %#v, want one successful result", i, results)
		}
	}

	sizes := appender.sizes()
	if len(sizes) != 1 {
		t.Fatalf("append call sizes = %v, want one coalesced append call", sizes)
	}
	if sizes[0] != submissions {
		t.Fatalf("coalesced append size = %d, want %d", sizes[0], submissions)
	}
}

func TestInboxCoalesceRespectsCanceledItem(t *testing.T) {
	group := New(Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		Appender:                  &orderedAppender{},
		MessageID:                 newBenchmarkMessageIDs(1),
		InboxCoalesceWindow:       20 * time.Millisecond,
		InboxCoalesceMaxItems:     8,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	target := benchmarkAuthorityTarget("coalesce-cancel")
	itemCtx, cancelItem := context.WithCancel(context.Background())
	canceledItem := benchmarkSendItem("coalesce-cancel")
	canceledItem.Context = itemCtx
	first, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{canceledItem})
	if err != nil {
		t.Fatalf("first submit error = %v", err)
	}
	cancelItem()

	second, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("coalesce-cancel")})
	if err != nil {
		t.Fatalf("second submit error = %v", err)
	}

	firstResults, err := first.Wait(context.Background())
	if err != nil {
		t.Fatalf("first wait error = %v", err)
	}
	if len(firstResults) != 1 || !errors.Is(firstResults[0].Err, context.Canceled) {
		t.Fatalf("first result = %#v, want context.Canceled", firstResults)
	}

	secondResults, err := second.Wait(context.Background())
	if err != nil {
		t.Fatalf("second wait error = %v", err)
	}
	if len(secondResults) != 1 || secondResults[0].Err != nil || secondResults[0].Result.Reason != ReasonSuccess {
		t.Fatalf("second result = %#v, want success", secondResults)
	}
}

type recordingBatchAppenderForCoalesceTest struct {
	mu    sync.Mutex
	sizes []int
	seq   uint64
}

func (a *recordingBatchAppenderForCoalesceTest) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sizes = append(a.sizes, len(req.Messages))
	out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
	for i, msg := range req.Messages {
		a.seq++
		out.Items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: a.seq, Message: msg}
	}
	return out, nil
}

func (a *recordingBatchAppenderForCoalesceTest) sizes() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.sizes...)
}
```

- [ ] **Step 2: Run coalescing tests to verify they fail**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/writer_advance_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestInboxCoalesce' -count=1
```

Expected: FAIL because the group does not yet pass coalescing options into `writerPorts` and the writer does not wait to collect nearby inbox items.

- [ ] **Step 3: Pass coalescing options into writer ports**

In `internalv2/runtime/channelappend/writer.go`, extend `writerPorts` with:

```go
	inboxCoalesceWindow   time.Duration
	inboxCoalesceMaxItems int
```

In `internalv2/runtime/channelappend/group.go`, add the fields to `ports := writerPorts{...}`:

```go
		inboxCoalesceWindow:   opts.InboxCoalesceWindow,
		inboxCoalesceMaxItems: opts.InboxCoalesceMaxItems,
```

- [ ] **Step 4: Add writer-side coalescing helpers**

In `internalv2/runtime/channelappend/writer.go`, add these helpers near `hasRunnableWorkLocked`:

```go
func (w *channelWriter) inboxItemCountLocked() int {
	count := 0
	for _, batch := range w.inbox {
		count += len(batch.items)
	}
	return count
}

// waitForInboxCoalesce gives nearby same-channel submissions a tiny bounded
// window to join the current writer pass. It never holds channelWriter.mu
// while sleeping.
func (w *channelWriter) waitForInboxCoalesce() {
	window := w.ports.inboxCoalesceWindow
	maxItems := w.ports.inboxCoalesceMaxItems
	if window <= 0 || maxItems <= 1 {
		return
	}
	w.mu.Lock()
	shouldWait := len(w.inbox) > 0 && w.inboxItemCountLocked() < maxItems
	w.mu.Unlock()
	if !shouldWait {
		return
	}

	timer := time.NewTimer(window)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	interval := window / 4
	if interval <= 0 {
		interval = window
	}
	if interval > 50*time.Microsecond {
		interval = 50 * time.Microsecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var done <-chan struct{}
	if w.ports.runtimeCtx != nil {
		done = w.ports.runtimeCtx.Done()
	}
	for {
		select {
		case <-done:
			return
		case <-timer.C:
			return
		case <-ticker.C:
			w.mu.Lock()
			ready := len(w.inbox) == 0 || w.inboxItemCountLocked() >= maxItems
			w.mu.Unlock()
			if ready {
				return
			}
		}
	}
}
```

- [ ] **Step 5: Call the coalescing wait before draining inbox**

In both `advance` and `advanceAppendOnly`, add the wait as the first statement in each loop:

```go
	for {
		w.waitForInboxCoalesce()
		w.mu.Lock()
```

The rest of each loop remains the Task 2 take/prepare/admit flow.

- [ ] **Step 6: Verify coalescing and advance tests pass**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/group.go internalv2/runtime/channelappend/writer_advance_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestInboxCoalesce|TestWriterAdvance|TestAdvancePostCommitReusesEffectWithoutBleed' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit coalescing runtime behavior**

Run:

```bash
git add internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/group.go internalv2/runtime/channelappend/writer_advance_test.go
git commit -m "perf(channelappend): coalesce writer inbox bursts"
```

Expected: commit succeeds.

## Task 4: Benchmark Coverage For Old And New Paths

**Files:**
- Modify: `internalv2/runtime/channelappend/benchmark_test.go`

- [ ] **Step 1: Disable coalescing in existing submit/wait microbenchmarks**

In every existing benchmark `Options{...}` that calls `submitAndWaitBenchmark` or `submitAndWaitBenchmarkItems`, add:

```go
		InboxCoalesceWindow:       -time.Nanosecond,
```

Apply this to:
- `BenchmarkSubmitLocalHotChannel`
- `BenchmarkSubmitLocalHotChannelWriterPressureObserver`
- `BenchmarkSubmitLocalHotChannelBatch16`
- `BenchmarkSubmitLocalManyChannelsParallel`
- `BenchmarkSubmitLocalManyChannelsParallelWriterPressureObserver`
- `BenchmarkSubmitLocalHotChannelPostCommit`

The existing benchmarks continue to measure the immediate submit/wait hot path without a deliberate coalescing delay.

- [ ] **Step 2: Add a burst benchmark for coalescing**

Add this benchmark after `BenchmarkSubmitLocalHotChannelBatch16`:

```go
func BenchmarkSubmitLocalHotChannelCoalescedBurst(b *testing.B) {
	const burstSize = 16
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       250 * time.Microsecond,
		InboxCoalesceMaxItems:     burstSize,
	})
	target := benchmarkAuthorityTarget("bench-hot-coalesced")
	items := benchmarkSendItems("bench-hot-coalesced", burstSize)
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		futures := make([]*Future, burstSize)
		for j := 0; j < burstSize; j++ {
			future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{items[j]})
			if err != nil {
				b.Fatalf("submit %d error = %v", j, err)
			}
			futures[j] = future
		}
		for j, future := range futures {
			results, err := future.Wait(context.Background())
			if err != nil {
				b.Fatalf("wait %d error = %v", j, err)
			}
			if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
				b.Fatalf("results %d = %#v, want one successful result", j, results)
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}
```

- [ ] **Step 3: Run benchmark compile and smoke**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/benchmark_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE -bench 'BenchmarkSubmitLocalHotChannel(PostCommit|Batch16|CoalescedBurst)$|BenchmarkSubmitLocalManyChannelsParallel$' -benchtime=20x -count=1
```

Expected: PASS and all selected benchmark names print one result line.

- [ ] **Step 4: Commit benchmark updates**

Run:

```bash
git add internalv2/runtime/channelappend/benchmark_test.go
git commit -m "test(channelappend): benchmark coalesced writer bursts"
```

Expected: commit succeeds.

## Task 5: Flow Documentation And Package Verification

**Files:**
- Modify: `internalv2/runtime/channelappend/FLOW.md`

- [ ] **Step 1: Update writer execution documentation**

In `internalv2/runtime/channelappend/FLOW.md`, replace the paragraph that begins with `Accepted submit events are prepared inline on the writer advance path.` with:

```markdown
Accepted submit events enter a lightweight per-writer inbox. A scheduled writer
may wait for the tiny runtime-only `InboxCoalesceWindow` before draining a
small inbox, stopping earlier when the inbox reaches `InboxCoalesceMaxItems`
logical send items. This wait runs only in the already scheduled writer
goroutine, never in `SubmitLocal`, and never while holding `channelWriter.mu`.
Its purpose is to merge near-simultaneous same-channel submissions into larger
append batches without changing local authority ownership or durable ordering.

After the wait, the writer briefly locks to detach the current inbox, prepares
those accepted batches outside `channelWriter.mu`, and re-locks only to admit
the prepared outcomes into `channelState`. Rejected and idempotent items
complete their item-aligned future slots immediately with their
reason/error/result. Valid prepared items receive one message id and one server
timestamp. Before a prepared item can enter the pending queue, its canonical
prepared channel must still match the submitted `AuthorityTarget`;
request-scoped derivation or person-channel normalization that changes the
channel away from the target returns `ErrStaleRoute` for that item and creates
no state. Idempotency hits use the same canonical target validation before
their stored result can complete successfully, so stale routes cannot bypass
authority ownership by returning an older successful result. Matching prepared
items enter the writer state's pending queue in input order only when the
state's pending plus in-flight item count is below
`ChannelBacklogHighWatermark`; saturated channels complete those items with
`ErrChannelBusy` before they reach the append port.
```

- [ ] **Step 2: Run scoped package tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1
```

Expected: PASS.

- [ ] **Step 3: Run scoped race tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -race -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup|TestAdvancePostCommitReusesEffectWithoutBleed|TestWriterDeactivateLocked|TestInboxCoalesce|TestWriterAdvancePreparesOutsideWriterLock' -count=1
```

Expected: PASS. On macOS, a linker warning about malformed `LC_DYSYMTAB` from system libraries is acceptable if the Go test command exits successfully.

- [ ] **Step 4: Run benchmark smoke with selected hot paths**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalHotChannelCoalescedBurst|BenchmarkSubmitLocalManyChannelsParallel$' -benchtime=200x -count=3
```

Expected: PASS and print benchmark rows for all four selected benchmark families.

- [ ] **Step 5: Commit docs and final formatting**

Run:

```bash
git add internalv2/runtime/channelappend/FLOW.md
git commit -m "docs(channelappend): describe writer inbox coalescing"
```

Expected: commit succeeds.

## Task 6: Merge Back To Local Main

**Files:**
- No file edits.

- [ ] **Step 1: Confirm the feature worktree is clean**

Run:

```bash
git status --short
git log --oneline -5
```

Expected:
- `git status --short` is empty in the feature worktree.
- The last commits include the option, prepare, coalescing, benchmark, and docs commits from this plan.

- [ ] **Step 2: Merge into local main**

Run from the feature worktree:

```bash
cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM
git status --short
git switch main
git merge --ff-only codex/channelappend-coalescing-fix
```

Expected:
- Existing unrelated untracked files remain untracked.
- `main` fast-forwards to the coalescing work.

- [ ] **Step 3: Clean up the isolated worktree and branch**

Run:

```bash
git worktree remove ../WuKongIM-channelappend-coalescing-fix
git branch -d codex/channelappend-coalescing-fix
```

Expected:
- Worktree removal succeeds.
- Branch deletion succeeds because `main` contains the branch tip.

## Final Verification Summary

Record these outcomes in the final response:

```text
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1
GOWORK=off go test ./internalv2/runtime/channelappend/ -race -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup|TestAdvancePostCommitReusesEffectWithoutBleed|TestWriterDeactivateLocked|TestInboxCoalesce|TestWriterAdvancePreparesOutsideWriterLock' -count=1
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalHotChannelCoalescedBurst|BenchmarkSubmitLocalManyChannelsParallel$' -benchtime=200x -count=3
```

Also state whether any full-repo test was skipped. Full `go test ./...` is not required for this scoped hot-path runtime change unless the executor chooses to spend the time and separate pre-existing unrelated failures from this work.

## Self-Review Checklist

- Spec coverage:
  - Lock-held `prepareBatch` is removed by Task 2.
  - Conservative writer-side coalescing is added by Task 3.
  - `SubmitLocal` remains non-blocking because the wait lives only in `advance`.
  - Cancellation during the coalescing window is covered by `TestInboxCoalesceRespectsCanceledItem`.
  - Existing scheduling is left intact; no sticky worker mode is introduced.
  - External config is unchanged; runtime `Options` are enough for this pass.
- Placeholder scan:
  - No unresolved implementation markers remain in this plan.
  - Every code-changing step includes concrete code or a concrete replacement target.
- Type consistency:
  - `InboxCoalesceWindow` and `InboxCoalesceMaxItems` are defined on `Options`, copied into `writerPorts`, and read by `channelWriter.waitForInboxCoalesce`.
  - `writerRuntimeConfig` uses the same field names as `writerPorts`.
  - Test helpers use existing `MessageIDAllocator`, `Appender`, `Future`, `ReasonSuccess`, and benchmark item helpers.
