package channelappend

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSubmitLocalCreatesStateOnlyForLocalAuthority(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1)})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "room", Type: 2},
		ChannelKey:   "2:room",
		LeaderNodeID: 1,
		Epoch:        10,
		LeaderEpoch:  3,
	}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	if future == nil {
		t.Fatalf("future is nil")
	}
	waitFutureForTest(t, future)
	if !group.HasStateForTest(target.ChannelID) {
		t.Fatalf("authority state was not created")
	}
}

func TestRecipientAuthorityDispatchConcurrencyIsIndependentFromEffectWorkers(t *testing.T) {
	group := New(Options{
		LocalNodeID:                           1,
		EffectPoolSize:                        32,
		RecipientAuthorityDispatchConcurrency: 4,
		RecipientAuthorityResolver:            staticRecipientAuthorityResolverForRecipientTest{nodeID: 1},
		RecipientDeliveryEnqueuer:             &recordingRecipientEnqueuerForRecipientTest{},
		RecipientBatchSize:                    16,
	})

	if group.opts.EffectPoolSize != 32 {
		t.Fatalf("EffectPoolSize = %d, want 32", group.opts.EffectPoolSize)
	}
	if got := group.shards[0].ports.commit.recipientDispatchConcurrency; got != 4 {
		t.Fatalf("recipient delivery enqueue concurrency = %d, want 4 independent from effect workers", got)
	}
}

func TestAdvancePoolSizeIsIndependentFromAuthorityShards(t *testing.T) {
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 8,
		AdvancePoolSize:     3,
		EffectPoolSize:      5,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	if group.opts.AuthorityShardCount != 8 {
		t.Fatalf("AuthorityShardCount = %d, want 8", group.opts.AuthorityShardCount)
	}
	if group.opts.AdvancePoolSize != 3 {
		t.Fatalf("AdvancePoolSize = %d, want 3", group.opts.AdvancePoolSize)
	}
	if got := group.advancePool.capacity(); got != 3 {
		t.Fatalf("advance pool capacity = %d, want 3", got)
	}
	if got := len(group.shards); got != 8 {
		t.Fatalf("shard count = %d, want 8", got)
	}
}

func TestSubmitLocalRejectsRemoteAuthorityWithoutState(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 2}
	_, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if !errors.Is(err, ErrNotChannelAuthority) {
		t.Fatalf("SubmitLocal() error = %v, want ErrNotChannelAuthority", err)
	}
	if group.StateCountForTest() != 0 {
		t.Fatalf("remote authority created state")
	}
}

func TestSubmitLocalCanceledBeforeSubmitNeverEnqueues(t *testing.T) {
	for i := 0; i < 256; i++ {
		group := New(Options{LocalNodeID: 1})
		if err := group.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("u1", "room")})
		stateCount := group.StateCountForTest()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		stopErr := group.Stop(stopCtx)
		stopCancel()
		if stopErr != nil {
			t.Fatalf("Stop() error = %v", stopErr)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: SubmitLocal() error = %v, want context.Canceled", i, err)
		}
		if future != nil {
			t.Fatalf("iteration %d: future = %v, want nil", i, future)
		}
		if stateCount != 0 {
			t.Fatalf("iteration %d: canceled submit created %d states", i, stateCount)
		}
	}
}

func TestStartAfterStopKeepsAdmissionClosed(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")}); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitLocal() error = %v, want ErrRouteNotReady", err)
	}
}

func TestSubmitLocalIgnoresCallerCancellationAfterMailboxAcceptance(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, AdmissionCapacityPerShard: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}

	ctx, cancel := context.WithCancel(context.Background())
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v, want nil after admission", err)
	}
	started := appender.waitStarted(t)
	cancel()
	started.Release()

	if future == nil {
		t.Fatalf("future is nil")
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1, 1)
}

func TestSubmitLocalAdmissionCapacityIsShardLocal(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{
		LocalNodeID:                 1,
		AuthorityShardCount:         2,
		AdmissionCapacityPerShard:   1,
		MessageID:                   newSequenceIDsForPrepare(1),
		Appender:                    appender,
		ChannelBacklogHighWatermark: 16,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		for {
			select {
			case started := <-appender.startedC:
				started.Release()
			default:
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := group.Stop(ctx); err != nil {
					t.Fatalf("Stop() error = %v", err)
				}
				return
			}
		}
	})

	first := localTargetForAppendTest("admission-a")
	second := differentShardTargetForTest(t, group, first)
	firstFuture, err := group.SubmitLocal(context.Background(), first, []SendBatchItem{testSendItem("u1", first.ChannelID.ID)})
	if err != nil {
		t.Fatalf("first SubmitLocal() error = %v", err)
	}
	if firstFuture == nil {
		t.Fatalf("first future is nil")
	}
	firstStarted := appender.waitStarted(t)

	if _, err := group.SubmitLocal(context.Background(), first, []SendBatchItem{testSendItem("u2", first.ChannelID.ID)}); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("same-shard SubmitLocal() error = %v, want ErrBackpressured", err)
	}
	secondFuture, err := group.SubmitLocal(context.Background(), second, []SendBatchItem{testSendItem("u3", second.ChannelID.ID)})
	if err != nil {
		t.Fatalf("different-shard SubmitLocal() error = %v, want nil", err)
	}
	if secondFuture == nil {
		t.Fatalf("second future is nil")
	}
	firstStarted.Release()
	secondStarted := appender.waitStarted(t)
	secondStarted.Release()
	requireAppendSuccess(t, waitFutureForTest(t, firstFuture), 0, 1, 1)
	requireAppendSuccess(t, waitFutureForTest(t, secondFuture), 0, 2, 2)
}

func TestSubmitLocalFutureCompletesWithAppendResults(t *testing.T) {
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(1),
		Appender:    newRecordingAppenderForAppendTest(),
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	items := []SendBatchItem{
		testSendItem("u1", "room"),
		testSendItem("u2", "room"),
	}

	future, err := group.SubmitLocal(context.Background(), target, items)
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if len(results) != len(items) {
		t.Fatalf("results len = %d, want %d", len(results), len(items))
	}
	requireAppendSuccess(t, results, 0, 1, 1)
	requireAppendSuccess(t, results, 1, 2, 2)
}

func differentShardTargetForTest(t *testing.T, group *Group, first AuthorityTarget) AuthorityTarget {
	t.Helper()
	firstShard := group.shardForTarget(first)
	for i := 0; i < 1024; i++ {
		target := localTargetForAppendTest("admission-b-" + strconv.Itoa(i))
		if group.shardForTarget(target) != firstShard {
			return target
		}
	}
	t.Fatalf("could not find target on a different shard")
	return AuthorityTarget{}
}

func TestStopWaitsForAcceptedAppendAndClosesAdmission(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{LocalNodeID: 1, AdmissionCapacityPerShard: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	started := appender.waitStarted(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopC := make(chan error, 1)
	go func() { stopC <- group.Stop(stopCtx) }()
	select {
	case err := <-stopC:
		t.Fatalf("Stop() returned before accepted append drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u2", "room")}); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitLocal() error = %v, want ErrRouteNotReady", err)
	}
	started.Release()
	if err := <-stopC; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1, 1)
}

func TestSubmitLocalFullMailboxReturnsBackpressureAndStopDrainsAcceptedWork(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{LocalNodeID: 1, AdmissionCapacityPerShard: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	queuedFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("queued SubmitLocal() error = %v", err)
	}
	started := appender.waitStarted(t)

	fullFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u2", "room")})
	if !errors.Is(err, ErrBackpressured) {
		t.Fatalf("full mailbox SubmitLocal() error = %v, want ErrBackpressured", err)
	}
	if fullFuture != nil {
		t.Fatalf("full mailbox future = %v, want nil", fullFuture)
	}

	started.Release()
	requireAppendSuccess(t, waitFutureForTest(t, queuedFuture), 0, 1, 1)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u3", "room")}); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitLocal() after Stop error = %v, want ErrRouteNotReady", err)
	}
}

func TestStopDeadlineKeepsAdmissionClosedAndPreservesAcceptedWork(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, LeaderNodeID: 1}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	started := appender.waitStarted(t)

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()
	if err := group.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context deadline exceeded", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() error = %v, want ErrBackpressured", err)
	}
	if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("u1", "room")}); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitLocal() error = %v, want ErrRouteNotReady", err)
	}

	started.Release()
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1, 1)
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := group.Stop(drainCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if err := group.Start(context.Background()); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("Start() after drain error = %v, want ErrBackpressured", err)
	}
}

func newStartedTestGroup(t *testing.T, opts Options) *Group {
	t.Helper()

	group := New(opts)
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	return group
}

func testSendItem(uid, channelID string) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     uid,
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: uid + "-msg",
			Payload:     []byte("hello"),
		},
	}
}

func (g *Group) HasStateForTest(channelID ChannelID) bool {
	writer := g.writerForTest(channelID)
	return writer != nil && writer.materializedStateForTest()
}

func (g *Group) StateCountForTest() int {
	count := 0
	for _, shard := range g.shards {
		shard.mu.RLock()
		for _, writer := range shard.writers {
			if writer.materializedStateForTest() {
				count++
			}
		}
		shard.mu.RUnlock()
	}
	return count
}

func (g *Group) writerForTest(channelID ChannelID) *channelWriter {
	key := channelKey(channelID)
	for _, shard := range g.shards {
		shard.mu.RLock()
		writer := shard.writers[key]
		shard.mu.RUnlock()
		if writer != nil {
			return writer
		}
	}
	return nil
}

func (w *channelWriter) materializedStateForTest() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.inbox) > 0 ||
		len(w.state.pendingItems) > 0 ||
		w.state.appendInflight > 0 ||
		w.state.appendInflightItems > 0 ||
		w.state.nextAppendSeq > 0 ||
		w.state.nextAppendDrainSeq > 0 ||
		w.state.hasReadyAppendCompletion ||
		len(w.state.completedAppends) > 0 ||
		w.state.commitBacklog() > 0 ||
		w.state.nextCommitSeq > 0 ||
		w.state.subscriberCache.ready
}

type submitResult struct {
	future *Future
	err    error
}

func receiveSubmitResult(t *testing.T, resultC <-chan submitResult) submitResult {
	t.Helper()
	select {
	case result := <-resultC:
		return result
	case <-time.After(time.Second):
		t.Fatalf("SubmitLocal did not return")
		return submitResult{}
	}
}

func waitFutureForTest(t *testing.T, future *Future) []SendBatchItemResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Future.Wait() error = %v", err)
	}
	return results
}
