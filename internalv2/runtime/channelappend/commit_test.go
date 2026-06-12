package channelappend

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

func TestAppendSuccessEnqueuesCommittedEventsAndSendackCompletesBeforeRecipientEffects(t *testing.T) {
	enqueuer := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	t.Cleanup(enqueuer.release)
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(900),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "payload")
	item.Command.MessageScopedUIDs = []string{"u2"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	enqueuer.waitStarted(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	results, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Future.Wait() while recipient delivery enqueue is blocked error = %v", err)
	}
	requireAppendSuccess(t, results, 0, 900, 1)

	committed := committedForAppendTest(t, group, target.ChannelID)
	if len(committed) != 1 {
		t.Fatalf("committed events = %d, want 1", len(committed))
	}
	if committed[0].MessageID != 900 || committed[0].MessageSeq != 1 {
		t.Fatalf("committed id/seq = %d/%d, want 900/1", committed[0].MessageID, committed[0].MessageSeq)
	}
}

func TestNoopPostCommitPortsDoNotScheduleCommitEffect(t *testing.T) {
	observer := &recordingEffectObserverForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(901),
		Appender:    newRecordingAppenderForAppendTest(),
		Observer:    observer,
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{appendSendItemForTest("u1", "room", "payload")})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 901, 1)
	time.Sleep(20 * time.Millisecond)

	if observer.hasStage(effectStagePostCommit) {
		t.Fatalf("observed %q effect for no-op post-commit ports", effectStagePostCommit)
	}
}

func TestCommitEffectsBatchSameChannelBacklog(t *testing.T) {
	observer := &recordingEffectObserverForCommitTest{}
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(925),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
		Observer:                   observer,
	})
	target := localTargetForAppendTest("room")
	items := []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u1", "room", "two"),
		appendSendItemForTest("u1", "room", "three"),
	}
	for i := range items {
		items[i].Command.MessageScopedUIDs = []string{"u2"}
	}

	future, err := group.SubmitLocal(context.Background(), target, items)
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	for i := range items {
		requireAppendSuccess(t, waitFutureForTest(t, future), i, uint64(925+i), uint64(i+1))
	}

	enqueuer.waitCalls(t, 3)
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
	if got := enqueuer.messageIDs(); !reflect.DeepEqual(got, []uint64{925, 926, 927}) {
		t.Fatalf("recipient delivery message ids = %#v, want ordered batch", got)
	}
	if got := observer.stageCount(effectStagePostCommit); got != 1 {
		t.Fatalf("post-commit effect observations = %d, want one batched effect", got)
	}
}

func TestAppendOmitResultPayloadStillDispatchesOriginalPayload(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(950),
		Appender:                   appender,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "original-payload")
	item.Command.MessageScopedUIDs = []string{"u2"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 950, 1)
	enqueuer.waitCalls(t, 1)

	requests := appender.Requests()
	if len(requests) != 1 {
		t.Fatalf("append requests = %d, want 1", len(requests))
	}
	if !requests[0].OmitResultPayload {
		t.Fatalf("OmitResultPayload = false, want true to avoid payload copy in append result")
	}
	payloads := enqueuer.payloads()
	if len(payloads) != 1 || string(payloads[0]) != "original-payload" {
		t.Fatalf("recipient payloads = %q, want original payload dispatched after omitted append result", payloads)
	}
}

func TestCommitEffectFailureDropsAndAdvancesWithoutRetry(t *testing.T) {
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{errs: []error{errors.New("temporary dispatch failure"), nil}}
	observer := &recordingPostCommitFailureObserverForTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1000),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
		Observer:                   observer,
	})
	target := localTargetForAppendTest("room")

	first := appendSendItemForTest("u1", "room", "one")
	first.Command.MessageScopedUIDs = []string{"u2"}
	second := appendSendItemForTest("u1", "room", "two")
	second.Command.MessageScopedUIDs = []string{"u3"}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first, second})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1000, 1)
	requireAppendSuccess(t, waitFutureForTest(t, future), 1, 1001, 2)

	enqueuer.waitCalls(t, 2)
	if got := enqueuer.messageIDs(); len(got) != 2 || got[0] != 1000 || got[1] != 1001 {
		t.Fatalf("recipient delivery enqueue message ids = %#v, want failed first dropped then second", got)
	}
	observer.waitFailures(t, 1)
	if got := observer.failures[0]; got.MessageID != 1000 || got.MessageSeq != 1 || got.Result != channelAppendResultOther {
		t.Fatalf("post-commit failure observation = %#v, want first message failure", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestCommitEffectFailureDoesNotRetryLater(t *testing.T) {
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{errs: []error{errors.New("temporary dispatch failure")}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1050),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "one")
	item.Command.MessageScopedUIDs = []string{"u2"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1050, 1)

	enqueuer.waitCalls(t, 1)
	time.Sleep(30 * time.Millisecond)
	if got := enqueuer.callCount(); got != 1 {
		t.Fatalf("recipient delivery enqueue calls after failure = %d, want no retry", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestCommitEffectFailuresDropThenAdvance(t *testing.T) {
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{errs: []error{errors.New("first failure"), errors.New("second failure"), nil}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1100),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
	})
	target := localTargetForAppendTest("room")

	first := appendSendItemForTest("u1", "room", "one")
	first.Command.MessageScopedUIDs = []string{"u2"}
	second := appendSendItemForTest("u1", "room", "two")
	second.Command.MessageScopedUIDs = []string{"u3"}
	third := appendSendItemForTest("u1", "room", "three")
	third.Command.MessageScopedUIDs = []string{"u4"}
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first, second, third})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1100, 1)
	requireAppendSuccess(t, waitFutureForTest(t, future), 1, 1101, 2)
	requireAppendSuccess(t, waitFutureForTest(t, future), 2, 1102, 3)

	enqueuer.waitCalls(t, 3)
	if got := enqueuer.messageIDs(); len(got) != 3 || got[0] != 1100 || got[1] != 1101 || got[2] != 1102 {
		t.Fatalf("recipient delivery enqueue message ids = %#v, want failures dropped then next events", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestNonLargeGroupSubscriberSnapshotCachedInChannelState(t *testing.T) {
	source := &recordingSubscriberSourceForRecipientTest{
		pages: []SubscriberPage{{Recipients: []Recipient{{UID: "u2"}, {UID: "u3"}}, Done: true}},
	}
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1150),
		Appender:                   newRecordingAppenderForAppendTest(),
		Subscribers:                source,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
		SubscriberScanPageSize:     1,
	})
	target := localTargetForAppendTest("room")
	target.SubscriberMutationVersion = 7

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u1", "room", "two"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1150, 1)
	requireAppendSuccess(t, waitFutureForTest(t, future), 1, 1151, 2)

	enqueuer.waitCalls(t, 2)
	if source.calls != 1 {
		t.Fatalf("subscriber source calls = %d, want one cached non-large snapshot load", source.calls)
	}
	if got := enqueuer.recipientUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3", "u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want cached subscribers dispatched for both messages", got)
	}
	if !reflect.DeepEqual(source.limits, []int{subscriberSnapshotLoadLimit}) {
		t.Fatalf("subscriber load limits = %#v, want one snapshot load", source.limits)
	}
}

func TestSubscriberMutationUpdatePatchesCachedSnapshot(t *testing.T) {
	source := &recordingSubscriberSourceForRecipientTest{
		pages: []SubscriberPage{{Recipients: []Recipient{{UID: "u2"}, {UID: "u3"}}, Done: true}},
	}
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1170),
		Appender:                   newRecordingAppenderForAppendTest(),
		Subscribers:                source,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
		SubscriberScanPageSize:     1,
	})
	target := localTargetForAppendTest("room")
	target.SubscriberMutationVersion = 1

	first, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal(first) error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, first), 0, 1170, 1)
	enqueuer.waitCalls(t, 1)

	if err := group.ApplySubscriberMutation(context.Background(), SubscriberMutationUpdate{
		ChannelID:                 target.ChannelID,
		SubscriberMutationVersion: 2,
		AddedUIDs:                 []string{"u4"},
		RemovedUIDs:               []string{"u2"},
	}); err != nil {
		t.Fatalf("ApplySubscriberMutation() error = %v", err)
	}
	target.SubscriberMutationVersion = 2
	second, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "two"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal(second) error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, second), 0, 1171, 2)
	enqueuer.waitCalls(t, 2)

	if source.calls != 1 {
		t.Fatalf("subscriber source calls = %d, want cached snapshot patched without reload", source.calls)
	}
	if got := enqueuer.recipientUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3", "u3", "u4"}) {
		t.Fatalf("recipient uids = %#v, want patched cached subscribers", got)
	}
}

func TestLargeGroupSubscribersRemainPagedPerCommittedMessage(t *testing.T) {
	source := &recordingSubscriberSourceForRecipientTest{
		pages: []SubscriberPage{
			{Recipients: []Recipient{{UID: "u2"}}, Cursor: "next"},
			{Recipients: []Recipient{{UID: "u3"}}, Done: true},
			{Recipients: []Recipient{{UID: "u2"}}, Cursor: "next"},
			{Recipients: []Recipient{{UID: "u3"}}, Done: true},
		},
	}
	enqueuer := &scriptedRecipientDeliveryEnqueuerForCommitTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1160),
		Appender:                   newRecordingAppenderForAppendTest(),
		Subscribers:                source,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
		SubscriberScanPageSize:     1,
	})
	target := localTargetForAppendTest("room")
	target.Large = true
	target.SubscriberMutationVersion = 9

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u1", "room", "two"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1160, 1)
	requireAppendSuccess(t, waitFutureForTest(t, future), 1, 1161, 2)

	enqueuer.waitCalls(t, 4)
	if source.calls != 4 {
		t.Fatalf("subscriber page calls = %d, want two pages per large-group message", source.calls)
	}
	if !reflect.DeepEqual(source.limits, []int{1, 1, 1, 1}) {
		t.Fatalf("subscriber page limits = %#v, want configured page size for large groups", source.limits)
	}
	if got := enqueuer.recipientUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3", "u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want paged subscribers for both messages", got)
	}
}

func TestBlockedCommitBacklogBackpressuresLaterSubmit(t *testing.T) {
	enqueuer := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	t.Cleanup(enqueuer.release)
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                 1,
		MessageID:                   newSequenceIDsForPrepare(1200),
		Appender:                    newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver:  staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:   enqueuer,
		RecipientBatchSize:          16,
		ChannelBacklogHighWatermark: 1,
	})
	target := localTargetForAppendTest("room")
	first := appendSendItemForTest("u1", "room", "one")
	first.Command.MessageScopedUIDs = []string{"u2"}

	firstFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first})
	if err != nil {
		t.Fatalf("first SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, firstFuture), 0, 1200, 1)
	enqueuer.waitStarted(t)

	second := appendSendItemForTest("u1", "room", "two")
	second.Command.MessageScopedUIDs = []string{"u3"}
	secondFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{second})
	if err != nil {
		t.Fatalf("second SubmitLocal() error = %v", err)
	}
	results := waitFutureForTest(t, secondFuture)
	if !errors.Is(results[0].Err, ErrChannelBusy) {
		t.Fatalf("second result error = %v, want ErrChannelBusy from commit backlog pressure", results[0].Err)
	}
}

func TestStopDoesNotWalkCanceledCommitBacklog(t *testing.T) {
	enqueuer := newBlockingRecipientDeliveryEnqueuerForCommitTest()
	t.Cleanup(enqueuer.release)
	group := New(Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1300),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  enqueuer,
		RecipientBatchSize:         16,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := localTargetForAppendTest("room")
	items := []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u1", "room", "two"),
		appendSendItemForTest("u1", "room", "three"),
	}
	for i := range items {
		items[i].Command.MessageScopedUIDs = []string{"u2"}
	}
	future, err := group.SubmitLocal(context.Background(), target, items)
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	_ = waitFutureForTest(t, future)
	enqueuer.waitStarted(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := enqueuer.callCount(); got != 1 {
		t.Fatalf("recipient delivery enqueue calls after stop = %d, want only in-flight commit", got)
	}
}

type staticRecipientAuthorityResolverForCommitTest struct {
	nodeID uint64
}

type recordingEffectObserverForCommitTest struct {
	mu     sync.Mutex
	stages []string
}

func (o *recordingEffectObserverForCommitTest) AppendFinished(string, error, time.Duration) {}

func (o *recordingEffectObserverForCommitTest) ObserveChannelAppendEffect(event EffectObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stages = append(o.stages, event.Stage)
}

func (o *recordingEffectObserverForCommitTest) hasStage(stage string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, got := range o.stages {
		if got == stage {
			return true
		}
	}
	return false
}

func (o *recordingEffectObserverForCommitTest) stageCount(stage string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	var count int
	for _, got := range o.stages {
		if got == stage {
			count++
		}
	}
	return count
}

func (r staticRecipientAuthorityResolverForCommitTest) ResolveRecipientAuthority(_ context.Context, _ string) (RecipientAuthorityTarget, error) {
	return authority.Target{HashSlot: 1, SlotID: 101, LeaderNodeID: r.nodeID, RouteRevision: 1001, AuthorityEpoch: 1}, nil
}

type blockingRecipientDeliveryEnqueuerForCommitTest struct {
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
	releaseO sync.Once
	mu       sync.Mutex
	calls    int
}

func newBlockingRecipientDeliveryEnqueuerForCommitTest() *blockingRecipientDeliveryEnqueuerForCommitTest {
	return &blockingRecipientDeliveryEnqueuerForCommitTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *blockingRecipientDeliveryEnqueuerForCommitTest) EnqueueRecipientBatch(ctx context.Context, _ RecipientAuthorityTarget, _ RecipientBatch) error {
	r.once.Do(func() {
		close(r.started)
	})
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case <-r.releaseC:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingRecipientDeliveryEnqueuerForCommitTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatalf("recipient delivery enqueue did not start")
	}
}

func (r *blockingRecipientDeliveryEnqueuerForCommitTest) release() {
	r.releaseO.Do(func() {
		close(r.releaseC)
	})
}

func (r *blockingRecipientDeliveryEnqueuerForCommitTest) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type scriptedRecipientDeliveryEnqueuerForCommitTest struct {
	mu      sync.Mutex
	cond    *sync.Cond
	errs    []error
	batches []RecipientBatch
}

type recordingPostCommitFailureObserverForTest struct {
	mu       sync.Mutex
	failures []PostCommitFailureObservation
}

func (o *recordingPostCommitFailureObserverForTest) AppendFinished(string, error, time.Duration) {}

func (o *recordingPostCommitFailureObserverForTest) ObserveChannelAppendPostCommitFailure(obs PostCommitFailureObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = append(o.failures, obs)
}

func (o *recordingPostCommitFailureObserverForTest) waitFailures(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		o.mu.Lock()
		got := len(o.failures)
		o.mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-commit failures = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) EnqueueRecipientBatch(_ context.Context, _ RecipientAuthorityTarget, batch RecipientBatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cond == nil {
		r.cond = sync.NewCond(&r.mu)
	}
	r.batches = append(r.batches, batch.Clone())
	r.cond.Broadcast()
	index := len(r.batches) - 1
	if index < len(r.errs) {
		return r.errs[index]
	}
	return nil
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) waitCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		if r.cond == nil {
			r.cond = sync.NewCond(&r.mu)
		}
		got := len(r.batches)
		r.mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recipient delivery enqueue calls = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) messageIDs() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint64, 0, len(r.batches))
	for _, batch := range r.batches {
		out = append(out, batch.Event.MessageID)
	}
	return out
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) recipientUIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, batch := range r.batches {
		for _, recipient := range batch.Recipients {
			out = append(out, recipient.UID)
		}
	}
	return out
}

func (r *scriptedRecipientDeliveryEnqueuerForCommitTest) payloads() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, 0, len(r.batches))
	for _, batch := range r.batches {
		out = append(out, append([]byte(nil), batch.Event.Payload...))
	}
	return out
}

func waitCommitBacklogForTest(t *testing.T, group *Group, channelID ChannelID, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if got := commitBacklogForTest(group, channelID); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("commit backlog = %d, want %d", commitBacklogForTest(group, channelID), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func commitBacklogForTest(group *Group, channelID ChannelID) int {
	writer := group.writerForTest(channelID)
	if writer == nil {
		return 0
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.state.commitBacklog()
}
