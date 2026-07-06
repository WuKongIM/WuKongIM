package channelappend

import (
	"context"
	"sync"
	"testing"
	"time"
)

type capturePressureObserver struct {
	mu        sync.Mutex
	seen      bool
	last      WriterPressureObservation
	antsSeen  map[string]AntsPoolObservation
	antsReady chan struct{}
	ready     chan struct{}
}

func newCapturePressureObserver() *capturePressureObserver {
	return &capturePressureObserver{
		antsSeen:  make(map[string]AntsPoolObservation),
		antsReady: make(chan struct{}),
		ready:     make(chan struct{}),
	}
}

func (c *capturePressureObserver) AppendFinished(string, error, time.Duration) {}

func (c *capturePressureObserver) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	c.mu.Lock()
	c.last = event
	if !c.seen && (event.PendingAppendItems > 0 || event.AppendInflightItems > 0) {
		c.seen = true
		close(c.ready)
	}
	c.mu.Unlock()
}

func (c *capturePressureObserver) ObserveChannelAppendAntsPool(event AntsPoolObservation) {
	c.mu.Lock()
	c.antsSeen[event.Pool] = event
	if len(c.antsSeen) >= 3 {
		select {
		case <-c.antsReady:
		default:
			close(c.antsReady)
		}
	}
	c.mu.Unlock()
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
	t.Cleanup(func() {
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
		if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("p1")}); err != nil {
			t.Fatalf("SubmitLocal error = %v", err)
		}
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
