package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriterAdvanceAppendsInOrder(t *testing.T) {
	appender := &orderedAppender{}
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: appender})
	defer rt.stop()

	target := benchmarkAuthorityTarget("order-1")
	const batches = 50
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("order-1")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = f
	}
	for i, f := range futures {
		res, err := f.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(res) != 1 || res[0].Err != nil || res[0].Result.Reason != ReasonSuccess {
			t.Fatalf("batch %d result = %#v, want success", i, res)
		}
	}
	if !appender.seqsMonotonic() {
		t.Fatalf("append message seqs not monotonic: %v", appender.seqs())
	}
}

func TestWriterAdvanceCompletesWithinDeadline(t *testing.T) {
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: &orderedAppender{}})
	defer rt.stop()
	target := benchmarkAuthorityTarget("deadline-1")
	f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("deadline-1")})
	if err != nil {
		t.Fatalf("submit error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.Wait(ctx); err != nil {
		t.Fatalf("wait error = %v", err)
	}
}

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

func TestInboxCoalesceWaitsForLateInboxItems(t *testing.T) {
	target := benchmarkAuthorityTarget("coalesce-wait")
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1})
	w.ports = writerPorts{
		runtimeCtx:            context.Background(),
		inboxCoalesceWindow:   200 * time.Millisecond,
		inboxCoalesceMaxItems: 3,
	}
	w.mu.Lock()
	w.inbox = []submittedBatch{{
		target: target,
		items:  []SendBatchItem{benchmarkSendItem("coalesce-wait")},
		future: newFuture(1),
	}}
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		w.waitForInboxCoalesce()
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	select {
	case <-done:
		t.Fatalf("waitForInboxCoalesce returned before max items or timer expiry")
	default:
	}
	w.mu.Lock()
	w.inbox = append(w.inbox,
		submittedBatch{target: target, items: []SendBatchItem{benchmarkSendItem("coalesce-wait")}, future: newFuture(1)},
		submittedBatch{target: target, items: []SendBatchItem{benchmarkSendItem("coalesce-wait")}, future: newFuture(1)},
	)
	w.mu.Unlock()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("waitForInboxCoalesce did not return after inbox reached max items")
	}

	w.mu.Lock()
	got := w.inboxItemCountLocked()
	w.mu.Unlock()
	if got != 3 {
		t.Fatalf("inbox item count = %d, want 3", got)
	}
}

func TestInboxCoalesceReturnsOnRuntimeCancel(t *testing.T) {
	target := benchmarkAuthorityTarget("coalesce-runtime-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1})
	w.ports = writerPorts{
		runtimeCtx:            ctx,
		inboxCoalesceWindow:   time.Second,
		inboxCoalesceMaxItems: 4,
	}
	w.mu.Lock()
	w.inbox = []submittedBatch{{
		target: target,
		items:  []SendBatchItem{benchmarkSendItem("coalesce-runtime-cancel")},
		future: newFuture(1),
	}}
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		w.waitForInboxCoalesce()
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	select {
	case <-done:
		t.Fatalf("waitForInboxCoalesce returned before runtime cancellation")
	default:
	}
	cancel()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("waitForInboxCoalesce did not return after runtime cancellation")
	}
}

func TestInboxCoalesceReturnsOnWindowExpiry(t *testing.T) {
	target := benchmarkAuthorityTarget("coalesce-expiry")
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1})
	w.ports = writerPorts{
		runtimeCtx:            context.Background(),
		inboxCoalesceWindow:   20 * time.Millisecond,
		inboxCoalesceMaxItems: 4,
	}
	w.mu.Lock()
	w.inbox = []submittedBatch{{
		target: target,
		items:  []SendBatchItem{benchmarkSendItem("coalesce-expiry")},
		future: newFuture(1),
	}}
	w.mu.Unlock()

	start := time.Now()
	w.waitForInboxCoalesce()
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("waitForInboxCoalesce returned after %s, want it to wait for the coalescing window", elapsed)
	}
}

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
	mu        sync.Mutex
	callSizes []int
	seq       uint64
}

func (a *recordingBatchAppenderForCoalesceTest) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.callSizes = append(a.callSizes, len(req.Messages))
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
	return append([]int(nil), a.callSizes...)
}

func TestAdvancePostCommitReusesEffectWithoutBleed(t *testing.T) {
	group := New(Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		Appender:                   &orderedAppender{},
		MessageID:                  newBenchmarkMessageIDs(1),
		ConversationActiveAdmitter: benchmarkNoopActiveAdmitter{},
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

	target := benchmarkAuthorityTarget("reuse-1")
	const batches = 200
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("reuse-1")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = f
	}
	for i, f := range futures {
		res, err := f.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(res) != 1 || res[0].Err != nil || res[0].Result.Reason != ReasonSuccess {
			t.Fatalf("batch %d result = %#v, want one successful result", i, res)
		}
	}
}
