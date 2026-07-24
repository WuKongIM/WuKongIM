package channelappend

import (
	"context"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capturePressureObserver struct {
	mu                          sync.Mutex
	seen                        bool
	last                        WriterPressureObservation
	maxPostCommitHandoffDepth   int
	maxPostCommitRetryQueue     int
	sawPostCommitRetryContended bool
	maxAdvanceWaiting           int
	lastAdvance                 AntsPoolObservation
	antsSeen                    map[string]AntsPoolObservation
	antsReady                   chan struct{}
	ready                       chan struct{}
}

type blockingPressureObserverForTest struct {
	mu           sync.Mutex
	first        bool
	firstStarted chan struct{}
	releaseFirst chan struct{}
	releaseOnce  sync.Once
	events       []WriterPressureObservation
}

func newCapturePressureObserver() *capturePressureObserver {
	return &capturePressureObserver{
		antsSeen:  make(map[string]AntsPoolObservation),
		antsReady: make(chan struct{}),
		ready:     make(chan struct{}),
	}
}

func newBlockingPressureObserverForTest() *blockingPressureObserverForTest {
	return &blockingPressureObserverForTest{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (o *blockingPressureObserverForTest) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	o.mu.Lock()
	first := !o.first
	if first {
		o.first = true
	}
	o.mu.Unlock()
	if first {
		close(o.firstStarted)
		<-o.releaseFirst
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *blockingPressureObserverForTest) snapshot() []WriterPressureObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]WriterPressureObservation(nil), o.events...)
}

func (o *blockingPressureObserverForTest) release() {
	o.releaseOnce.Do(func() { close(o.releaseFirst) })
}

func (c *capturePressureObserver) AppendFinished(string, error, time.Duration) {}

func (c *capturePressureObserver) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	c.mu.Lock()
	c.last = event
	if event.PostCommitHandoffDepth > c.maxPostCommitHandoffDepth {
		c.maxPostCommitHandoffDepth = event.PostCommitHandoffDepth
	}
	if event.PostCommitRetryQueueDepth > c.maxPostCommitRetryQueue {
		c.maxPostCommitRetryQueue = event.PostCommitRetryQueueDepth
	}
	c.sawPostCommitRetryContended = c.sawPostCommitRetryContended || event.PostCommitRetryContended
	if !c.seen && (event.PendingAppendItems > 0 || event.AppendInflightItems > 0) {
		c.seen = true
		close(c.ready)
	}
	c.mu.Unlock()
}

func (c *capturePressureObserver) postCommitSnapshot() (WriterPressureObservation, int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.maxPostCommitHandoffDepth, c.maxPostCommitRetryQueue, c.sawPostCommitRetryContended
}

func (c *capturePressureObserver) ObserveChannelAppendAntsPool(event AntsPoolObservation) {
	c.mu.Lock()
	c.antsSeen[event.Pool] = event
	if event.Pool == "advance" {
		c.lastAdvance = event
		if event.Waiting > c.maxAdvanceWaiting {
			c.maxAdvanceWaiting = event.Waiting
		}
	}
	if len(c.antsSeen) >= 3 {
		select {
		case <-c.antsReady:
		default:
			close(c.antsReady)
		}
	}
	c.mu.Unlock()
}

func (c *capturePressureObserver) advanceSnapshot() (AntsPoolObservation, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastAdvance, c.maxAdvanceWaiting
}

func TestGroupEmitsAggregatePressure(t *testing.T) {
	observer := newCapturePressureObserver()
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 4,
		EffectPoolSize:      5,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            appender,
		Observer:            observer,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	var futures []*Future
	t.Cleanup(func() {
		releaseDone := make(chan struct{})
		go func() {
			for {
				select {
				case started := <-appender.startedC:
					started.Release()
				case <-releaseDone:
					return
				}
			}
		}()
		defer close(releaseDone)
		for _, future := range futures {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err := future.Wait(ctx)
			cancel()
			if err != nil {
				t.Fatalf("Future.Wait() during cleanup error = %v", err)
			}
		}
		for {
			select {
			case started := <-appender.startedC:
				started.Release()
			default:
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := group.Stop(ctx); err != nil {
					t.Fatalf("Stop error = %v", err)
				}
				return
			}
		}
	})

	target := benchmarkAuthorityTarget("p1")
	for i := 0; i < 5; i++ {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("p1")})
		if err != nil {
			t.Fatalf("SubmitLocal error = %v", err)
		}
		futures = append(futures, future)
	}

	select {
	case <-observer.ready:
	case <-time.After(2 * time.Second):
		observer.mu.Lock()
		last := observer.last
		observer.mu.Unlock()
		t.Fatalf("no pressure observation with pending/in-flight work; last = %#v", last)
	}
	select {
	case <-observer.antsReady:
	case <-time.After(2 * time.Second):
		observer.mu.Lock()
		seen := observer.antsSeen
		observer.mu.Unlock()
		t.Fatalf("no ants pool observations; seen = %#v", seen)
	}
	observer.mu.Lock()
	advance := observer.antsSeen["advance"]
	appendEffect := observer.antsSeen["append_effect"]
	postCommit := observer.antsSeen["post_commit"]
	observer.mu.Unlock()
	if advance.Capacity == 0 || appendEffect.Capacity != 5 || postCommit.Capacity != 5 {
		t.Fatalf("ants pool observations = advance %#v append_effect %#v post_commit %#v, want advance, append_effect, and post_commit pools", advance, appendEffect, postCommit)
	}
}

type blockingAdvanceAuthorizerForPressureTest struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAdvanceAuthorizerForPressureTest() *blockingAdvanceAuthorizerForPressureTest {
	return &blockingAdvanceAuthorizerForPressureTest{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (a *blockingAdvanceAuthorizerForPressureTest) AuthorizeSend(ctx context.Context, _ SendCommand) (Decision, error) {
	a.once.Do(func() { close(a.started) })
	select {
	case <-a.release:
		return Decision{Allowed: true, Reason: ReasonSuccess}, nil
	case <-ctx.Done():
		return Decision{}, ctx.Err()
	}
}

func TestAdvanceDispatcherPublishesQueuedAndDrainedPressure(t *testing.T) {
	observer := newCapturePressureObserver()
	authorizer := newBlockingAdvanceAuthorizerForPressureTest()
	group := New(Options{
		LocalNodeID:                 1,
		AuthorityShardCount:         1,
		AdvancePoolSize:             1,
		EffectPoolSize:              1,
		AdmissionCapacityPerShard:   8,
		ChannelBacklogHighWatermark: 8,
		MessageID:                   newSequenceIDsForPrepare(1),
		Authorizer:                  authorizer,
		Appender:                    newRecordingAppenderForAppendTest(),
		Observer:                    observer,
		InboxCoalesceWindow:         -time.Nanosecond,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(authorizer.release) })
	}
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	futures := make([]*Future, 0, 3)
	submit := func(index int) {
		channelID := "advance-pressure-" + strconv.Itoa(index)
		future, err := group.SubmitLocal(context.Background(), AuthorityTarget{
			ChannelID:    ChannelID{ID: channelID, Type: 2},
			ChannelKey:   "2:" + channelID,
			LeaderNodeID: 1,
			Epoch:        1,
			LeaderEpoch:  1,
		}, []SendBatchItem{testSendItem("u"+strconv.Itoa(index), channelID)})
		if err != nil {
			t.Fatalf("SubmitLocal(%d) error = %v", index, err)
		}
		futures = append(futures, future)
	}
	submit(0)
	select {
	case <-authorizer.started:
	case <-time.After(time.Second):
		t.Fatal("first advance worker did not enter blocking authorizer")
	}
	submit(1)
	submit(2)

	deadline := time.Now().Add(time.Second)
	for {
		_, maxWaiting := observer.advanceSnapshot()
		if maxWaiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			last, maxWaiting := observer.advanceSnapshot()
			t.Fatalf("advance waiting max = %d, want at least 2 queued/dispatching writers; last = %#v", maxWaiting, last)
		}
		time.Sleep(time.Millisecond)
	}

	release()
	for _, future := range futures {
		requireAppendSuccessAnyID(t, waitFutureForTest(t, future), 0)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := group.Stop(stopCtx); err != nil {
		cancel()
		t.Fatalf("Stop() error = %v", err)
	}
	cancel()
	last, _ := observer.advanceSnapshot()
	if last.Waiting != 0 {
		t.Fatalf("terminal advance waiting = %d, want 0", last.Waiting)
	}
}

func TestWriterPressurePublicationCannotOverwriteDrainWithOlderSnapshot(t *testing.T) {
	observer := newBlockingPressureObserverForTest()
	handoff := newPostCommitHandoff(1)
	if !handoff.tryAcquire() {
		t.Fatal("tryAcquire() = false, want initial handoff reservation")
	}
	metrics := &groupMetrics{
		observer:          observer,
		handoff:           handoff,
		postCommitRetries: newPostCommitRetryScheduler(),
	}
	metrics.startPressurePublisher()
	t.Cleanup(func() {
		observer.release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := metrics.stopPressurePublisher(ctx); err != nil {
			t.Errorf("stopPressurePublisher() error = %v", err)
		}
	})

	metrics.observePressure()
	select {
	case <-observer.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first pressure publication did not start")
	}

	handoff.release(1)
	updatesDone := make(chan struct{})
	go func() {
		for range 10_000 {
			metrics.observePressure()
		}
		close(updatesDone)
	}()
	select {
	case <-updatesDone:
	case <-time.After(time.Second):
		t.Fatal("pressure updates blocked behind a slow observer")
	}
	observer.release()

	deadline := time.Now().Add(time.Second)
	for {
		events := observer.snapshot()
		if len(events) >= 2 {
			last := events[len(events)-1]
			if last.PostCommitHandoffDepth == 0 && last.PostCommitRetryQueueDepth == 0 && !last.PostCommitRetryContended {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pressure publications = %#v, want old snapshot followed by drained zero state", events)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPostCommitEffectDoesNotBlockDurableAppendPool(t *testing.T) {
	appender := newPostCommitIsolationAppenderForPressureTest("append")
	delivery := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1),
		Appender:                   appender,
		EffectPoolSize:             1,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  delivery,
		RecipientBatchSize:         16,
	})
	t.Cleanup(delivery.release)

	postCommitTarget := localTargetForAppendTest("post-commit")
	postCommitItem := appendSendItemForTest("u1", postCommitTarget.ChannelID.ID, "post-commit")
	postCommitItem.Command.MessageScopedUIDs = []string{"u2"}
	future, err := group.SubmitLocal(context.Background(), postCommitTarget, []SendBatchItem{postCommitItem})
	if err != nil {
		t.Fatalf("SubmitLocal(post-commit) error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1, 1)
	delivery.waitStarted(t)

	appendTarget := localTargetForAppendTest("append")
	appendC := submitNoWaitForAppendTest(group, appendTarget, appendSendItemForTest("u1", appendTarget.ChannelID.ID, "append"))
	appendStarted := appender.waitBlockedAppendStarted(t)
	appendStarted.Release()

	appendResult := receiveSubmitResult(t, appendC)
	if appendResult.err != nil {
		t.Fatalf("SubmitLocal(append) error = %v", appendResult.err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, appendResult.future), 0, 2, 1)
	delivery.release()
	waitCommitBacklogForTest(t, group, postCommitTarget.ChannelID, 0)
}

func TestPostCommitSaturationRetainsDurableEffectsWithoutBlockingForegroundAppend(t *testing.T) {
	observer := newCapturePressureObserver()
	delivery := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(10),
		Appender:                   newRecordingAppenderForAppendTest(),
		AdvancePoolSize:            1,
		EffectPoolSize:             1,
		PostCommitHandoffCapacity:  8,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  delivery,
		RecipientBatchSize:         16,
		Observer:                   observer,
	})
	t.Cleanup(delivery.release)

	firstTarget := localTargetForAppendTest("post-commit-first")
	firstItem := appendSendItemForTest("u1", firstTarget.ChannelID.ID, "first")
	firstItem.Command.MessageScopedUIDs = []string{"u2"}
	first, err := group.SubmitLocal(context.Background(), firstTarget, []SendBatchItem{firstItem})
	if err != nil {
		t.Fatalf("SubmitLocal(first) error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, first), 0, 10, 1)
	delivery.waitStarted(t)

	secondTarget := localTargetForAppendTest("post-commit-second")
	secondItem := appendSendItemForTest("u1", secondTarget.ChannelID.ID, "second")
	secondItem.Command.MessageScopedUIDs = []string{"u2"}
	second, err := group.SubmitLocal(context.Background(), secondTarget, []SendBatchItem{secondItem})
	if err != nil {
		t.Fatalf("SubmitLocal(second) error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, second), 0, 11, 2)

	if backlog := commitBacklogForTest(group, secondTarget.ChannelID); backlog != 1 {
		t.Fatalf("second post-commit backlog = %d, want retained durable effect while pool is saturated", backlog)
	}
	waitPostCommitPressureForTest(t, observer, func(_ WriterPressureObservation, maxHandoff int, maxRetryQueue int, sawContended bool) bool {
		return maxHandoff >= 2 && maxRetryQueue >= 1 && sawContended
	}, "handoff depth >= 2, retry queue >= 1, and contended retry ownership")

	foregroundTarget := localTargetForAppendTest("foreground")
	foregroundItem := appendSendItemForTest("u1", foregroundTarget.ChannelID.ID, "foreground")
	foregroundItem.Command.MessageScopedUIDs = []string{"u2"}
	foregroundResult := receiveSubmitResult(t, submitNoWaitForAppendTest(group, foregroundTarget, foregroundItem))
	if foregroundResult.err != nil {
		t.Fatalf("SubmitLocal(foreground) error = %v", foregroundResult.err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, foregroundResult.future), 0, 12, 3)
	delivery.release()
	delivery.waitCalls(t, 3)
	waitCommitBacklogForTest(t, group, secondTarget.ChannelID, 0)
	waitCommitBacklogForTest(t, group, foregroundTarget.ChannelID, 0)
	if got := delivery.messageIDs(); !reflect.DeepEqual(got, []uint64{10, 11, 12}) {
		t.Fatalf("recipient delivery message ids = %#v, want durable messages in FIFO order exactly once", got)
	}
	waitPostCommitPressureForTest(t, observer, func(last WriterPressureObservation, _ int, _ int, _ bool) bool {
		return last.PostCommitHandoffDepth == 0 &&
			last.PostCommitHandoffCapacity == 8 &&
			last.PostCommitRetryQueueDepth == 0 &&
			!last.PostCommitRetryContended
	}, "handoff and retry pressure returned to zero")
}

func waitPostCommitPressureForTest(
	t *testing.T,
	observer *capturePressureObserver,
	ready func(WriterPressureObservation, int, int, bool) bool,
	want string,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		last, maxHandoff, maxRetryQueue, sawContended := observer.postCommitSnapshot()
		if ready(last, maxHandoff, maxRetryQueue, sawContended) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-commit pressure did not reach %s: last=%#v max_handoff=%d max_retry_queue=%d saw_contended=%t", want, last, maxHandoff, maxRetryQueue, sawContended)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPostCommitRetryBurstRefillsFreedWorkersBeforeFallbackTimer(t *testing.T) {
	const (
		poolSize       = 4
		queuedWriters  = 8
		firstMessageID = 2000
	)
	delivery := newBurstRefillRecipientEnqueuerForPressureTest(firstMessageID + poolSize)
	group := New(Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(firstMessageID),
		Appender:                   newRecordingAppenderForAppendTest(),
		EffectPoolSize:             poolSize,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  delivery,
		RecipientBatchSize:         16,
	})
	group.postCommitRetries.retryInterval = 500 * time.Millisecond
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		delivery.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	submit := func(channel string) {
		t.Helper()
		item := appendSendItemForTest("u1", channel, "payload")
		item.Command.MessageScopedUIDs = []string{"u2"}
		future, err := group.SubmitLocal(context.Background(), localTargetForAppendTest(channel), []SendBatchItem{item})
		if err != nil {
			t.Fatalf("SubmitLocal(%q) error = %v", channel, err)
		}
		requireResultReason(t, waitFutureForTest(t, future), 0, ReasonSuccess)
	}
	for i := 0; i < poolSize; i++ {
		submit("burst-occupier-" + string(rune('a'+i)))
	}
	delivery.waitInitialStarted(t, poolSize)
	for i := 0; i < queuedWriters; i++ {
		submit("burst-waiter-" + string(rune('a'+i)))
	}

	deadline := time.Now().Add(time.Second)
	for group.postCommitRetries.pending() != queuedWriters {
		if time.Now().After(deadline) {
			t.Fatalf("retry FIFO pending = %d, want %d sparse writers", group.postCommitRetries.pending(), queuedWriters)
		}
		time.Sleep(time.Millisecond)
	}

	delivery.releaseInitial()

	deadline = time.Now().Add(100 * time.Millisecond)
	for delivery.retryStartedCount() < poolSize {
		if time.Now().After(deadline) {
			t.Fatalf("retry burst started = %d, want %d freed workers refilled before fallback timer", delivery.retryStartedCount(), poolSize)
		}
		time.Sleep(time.Millisecond)
	}
	delivery.releaseRetries()
	waitHandoffDepthForTest(t, group, 0)
}

func TestPostCommitRetryTurnParksNewAppendAndRefillsNextWriterWhileAppendPoolSaturated(t *testing.T) {
	delivery := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	appender := newPostCommitIsolationAppenderForPressureTest("append-blocker")
	group := New(Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(3000),
		Appender:                   appender,
		AdvancePoolSize:            4,
		EffectPoolSize:             1,
		PostCommitHandoffCapacity:  16,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  delivery,
		RecipientBatchSize:         16,
	})
	// Keep the retry FIFO stable until the real capacity completion below wakes it.
	group.postCommitRetries.retryInterval = 10 * time.Second
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	submitDurable := func(target AuthorityTarget, payload string) *Future {
		t.Helper()
		item := appendSendItemForTest("u1", target.ChannelID.ID, payload)
		item.Command.MessageScopedUIDs = []string{"u2"}
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
		if err != nil {
			t.Fatalf("SubmitLocal(%q) error = %v", target.ChannelID.ID, err)
		}
		return future
	}

	occupierTarget := localTargetForAppendTest("retry-occupier")
	requireResultReason(t, waitFutureForTest(t, submitDurable(occupierTarget, "occupier")), 0, ReasonSuccess)
	delivery.waitStarted(t)

	firstRetryTarget := localTargetForAppendTest("retry-first")
	requireResultReason(t, waitFutureForTest(t, submitDurable(firstRetryTarget, "first-old")), 0, ReasonSuccess)
	waitPostCommitRetryPendingForPressureTest(t, group, 1)
	secondRetryTarget := localTargetForAppendTest("retry-second")
	requireResultReason(t, waitFutureForTest(t, submitDurable(secondRetryTarget, "second-old")), 0, ReasonSuccess)
	waitPostCommitRetryPendingForPressureTest(t, group, 2)

	blockerTarget := localTargetForAppendTest("append-blocker")
	blockerFuture := submitDurable(blockerTarget, "blocker")
	blockedAppend := appender.waitBlockedAppendStarted(t)
	var releaseAppendOnce sync.Once
	t.Cleanup(func() {
		releaseAppendOnce.Do(blockedAppend.Release)
		delivery.release()
	})

	newFirstFuture := submitDurable(firstRetryTarget, "first-new")
	waitQueuedWriterAppendParkedOrBlockedForPressureTest(t, group, firstRetryTarget)
	if got := delivery.callCount(); got != 1 {
		t.Fatalf("delivery calls before freeing post-commit worker = %d, want only occupier", got)
	}

	delivery.release()
	deadline := time.Now().Add(time.Second)
	for group.postCommitPool.running() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("post-commit pool running = %d, want 0 after occupier release", group.postCommitPool.running())
		}
		time.Sleep(time.Millisecond)
	}

	// The first selected writer must admit its old commit before its new append,
	// then hand the FIFO turn to the second writer while appendPool is still full.
	delivery.waitCalls(t, 3)
	if got := delivery.messageIDs(); !reflect.DeepEqual(got, []uint64{3000, 3001, 3002}) {
		t.Fatalf("delivery message ids before append release = %#v, want occupier and both retry commits in FIFO order", got)
	}
	if got := group.appendPool.running(); got != 1 {
		t.Fatalf("append pool running = %d, want blocker still occupying the only worker", got)
	}

	releaseAppendOnce.Do(blockedAppend.Release)
	requireResultReason(t, waitFutureForTest(t, blockerFuture), 0, ReasonSuccess)
	requireResultReason(t, waitFutureForTest(t, newFirstFuture), 0, ReasonSuccess)
	waitHandoffDepthForTest(t, group, 0)
}

func waitPostCommitRetryPendingForPressureTest(t *testing.T, group *Group, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for group.postCommitRetries.pending() != want {
		if time.Now().After(deadline) {
			t.Fatalf("retry FIFO pending = %d, want %d", group.postCommitRetries.pending(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitQueuedWriterAppendParkedOrBlockedForPressureTest(t *testing.T, group *Group, target AuthorityTarget) {
	t.Helper()
	writer := group.writerForTest(target.ChannelID)
	if writer == nil {
		t.Fatalf("writer for %q is nil", target.ChannelID.ID)
	}
	deadline := time.Now().Add(time.Second)
	for {
		writer.mu.Lock()
		queued := writer.commitRetryQueued
		hasAppend := len(writer.inbox) > 0 || len(writer.state.pendingItems) > 0 || writer.state.appendInflight > 0
		writer.mu.Unlock()
		parked := !writer.scheduled.Load()
		blocked := group.appendPool.waiting() > 0
		if queued && hasAppend && (parked || blocked) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued writer did not park or block append: queued=%t has_append=%t scheduled=%t append_waiting=%d", queued, hasAppend, writer.scheduled.Load(), group.appendPool.waiting())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestApplyDefaultsClampsWorkerPoolsToAntsCapacity(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	opts := applyDefaults(Options{
		AdvancePoolSize:             maxInt,
		EffectPoolSize:              maxInt,
		ChannelBacklogHighWatermark: 1024,
	})

	if opts.AdvancePoolSize != maxWorkerPoolSize || opts.EffectPoolSize != maxWorkerPoolSize {
		t.Fatalf("worker pool sizes = advance %d effect %d, want ants capacity %d", opts.AdvancePoolSize, opts.EffectPoolSize, maxWorkerPoolSize)
	}
	wantHandoff := maxInt
	if maxWorkerPoolSize <= maxInt/commitBatchMaxEvents {
		wantHandoff = maxWorkerPoolSize * commitBatchMaxEvents
	}
	if opts.PostCommitHandoffCapacity != wantHandoff {
		t.Fatalf("pool-derived PostCommitHandoffCapacity = %d, want %d", opts.PostCommitHandoffCapacity, wantHandoff)
	}

	pool := newNonblockingWorkerPool(maxInt)
	if got := pool.capacity(); got != maxWorkerPoolSize {
		t.Fatalf("worker pool capacity = %d, want %d", got, maxWorkerPoolSize)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.stop(ctx); err != nil {
		t.Fatalf("worker pool stop error = %v", err)
	}
}

func TestGroupDisablesWriterPressureMetricsWithoutObserver(t *testing.T) {
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 2,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            &recordingAppenderForAppendTest{},
		EffectPoolSize:      1,
	})

	for _, shard := range group.shards {
		if shard.ports.metrics != nil {
			t.Fatalf("writer pressure metrics = %p, want nil without pressure observer", shard.ports.metrics)
		}
	}
}

type postCommitIsolationAppenderForPressureTest struct {
	blockedChannel string
	blocked        *blockingAppenderForAppendTest
	recording      *recordingAppenderForAppendTest
}

type burstRefillRecipientEnqueuerForPressureTest struct {
	firstRetryMessageID uint64
	initialStarted      atomic.Int64
	retryStarted        atomic.Int64
	initialRelease      chan struct{}
	retryRelease        chan struct{}
	initialOnce         sync.Once
	retryOnce           sync.Once
}

func newBurstRefillRecipientEnqueuerForPressureTest(firstRetryMessageID uint64) *burstRefillRecipientEnqueuerForPressureTest {
	return &burstRefillRecipientEnqueuerForPressureTest{
		firstRetryMessageID: firstRetryMessageID,
		initialRelease:      make(chan struct{}),
		retryRelease:        make(chan struct{}),
	}
}

func (e *burstRefillRecipientEnqueuerForPressureTest) EnqueueRecipientBatch(ctx context.Context, _ RecipientAuthorityTarget, batch RecipientBatch) error {
	release := e.retryRelease
	if batch.Event.MessageID < e.firstRetryMessageID {
		e.initialStarted.Add(1)
		release = e.initialRelease
	} else {
		e.retryStarted.Add(1)
	}
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *burstRefillRecipientEnqueuerForPressureTest) waitInitialStarted(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for int(e.initialStarted.Load()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("initial post-commit effects started = %d, want %d", e.initialStarted.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *burstRefillRecipientEnqueuerForPressureTest) retryStartedCount() int {
	return int(e.retryStarted.Load())
}

func (e *burstRefillRecipientEnqueuerForPressureTest) releaseInitial() {
	e.initialOnce.Do(func() { close(e.initialRelease) })
}

func (e *burstRefillRecipientEnqueuerForPressureTest) releaseRetries() {
	e.retryOnce.Do(func() { close(e.retryRelease) })
}

func (e *burstRefillRecipientEnqueuerForPressureTest) releaseAll() {
	e.releaseInitial()
	e.releaseRetries()
}

func newPostCommitIsolationAppenderForPressureTest(blockedChannel string) *postCommitIsolationAppenderForPressureTest {
	return &postCommitIsolationAppenderForPressureTest{
		blockedChannel: blockedChannel,
		blocked:        newBlockingAppenderForAppendTest(),
		recording:      newRecordingAppenderForAppendTest(),
	}
}

func (a *postCommitIsolationAppenderForPressureTest) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	if req.ChannelID.ID == a.blockedChannel {
		return a.blocked.AppendBatch(ctx, req)
	}
	return a.recording.AppendBatch(ctx, req)
}

func (a *postCommitIsolationAppenderForPressureTest) waitBlockedAppendStarted(t *testing.T) appendStartedForAppendTest {
	t.Helper()
	return a.blocked.waitStarted(t)
}

func TestGroupEnablesWriterPressureMetricsWithObserver(t *testing.T) {
	observer := newCapturePressureObserver()
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 2,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            &recordingAppenderForAppendTest{},
		EffectPoolSize:      1,
		Observer:            observer,
	})

	for _, shard := range group.shards {
		if shard.ports.metrics == nil {
			t.Fatal("writer pressure metrics = nil, want enabled with pressure observer")
		}
	}
}
