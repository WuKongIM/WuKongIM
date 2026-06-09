package channelwrite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

func TestAppendSuccessEnqueuesCommittedEventsAndSendackCompletesBeforeRecipientEffects(t *testing.T) {
	router := newBlockingRecipientRouterForCommitTest()
	t.Cleanup(router.release)
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(900),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "payload")
	item.Command.MessageScopedUIDs = []string{"u2"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	router.waitStarted(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	results, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Future.Wait() while recipient dispatch is blocked error = %v", err)
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

func TestCommitEffectFailureRetriesSameEventBeforeNextEvent(t *testing.T) {
	router := &scriptedRecipientRouterForCommitTest{errs: []error{errors.New("temporary dispatch failure"), nil, nil}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1000),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		CommitRetryMaxAttempts:     3,
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

	router.waitCalls(t, 3)
	if got := router.messageIDs(); len(got) != 3 || got[0] != 1000 || got[1] != 1000 || got[2] != 1001 {
		t.Fatalf("recipient dispatch message ids = %#v, want retry first before second", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestCommitEffectTerminalFailureDropsThenAdvances(t *testing.T) {
	router := &scriptedRecipientRouterForCommitTest{errs: []error{errors.New("first failure"), errors.New("terminal failure"), nil}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1100),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		CommitRetryMaxAttempts:     2,
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
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 1100, 1)
	requireAppendSuccess(t, waitFutureForTest(t, future), 1, 1101, 2)

	router.waitCalls(t, 3)
	if got := router.messageIDs(); len(got) != 3 || got[0] != 1100 || got[1] != 1100 || got[2] != 1101 {
		t.Fatalf("recipient dispatch message ids = %#v, want terminal drop then next event", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestBlockedCommitBacklogBackpressuresLaterSubmit(t *testing.T) {
	router := newBlockingRecipientRouterForCommitTest()
	t.Cleanup(router.release)
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1200),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		PendingItemHighWatermark:   1,
	})
	target := localTargetForAppendTest("room")
	first := appendSendItemForTest("u1", "room", "one")
	first.Command.MessageScopedUIDs = []string{"u2"}

	firstFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first})
	if err != nil {
		t.Fatalf("first SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, firstFuture), 0, 1200, 1)
	router.waitStarted(t)

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
	router := newBlockingRecipientRouterForCommitTest()
	t.Cleanup(router.release)
	group := New(Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(1300),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
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
	router.waitStarted(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := router.callCount(); got != 1 {
		t.Fatalf("recipient dispatch calls after stop = %d, want only in-flight commit", got)
	}
}

func TestStopReleasesUnsentPendingCommitEffect(t *testing.T) {
	target := localTargetForAppendTest("room")
	key := targetKey(target)
	reactor := newReactor(
		0,
		1,
		channelStateLimits{},
		1,
		preparePorts{},
		appendPorts{},
		commitPorts{recipientRouter: &recordingRecipientRouterForRecipientTest{}},
		cursorPorts{},
	)
	state := newChannelState(target, channelStateLimits{})
	state.enqueueCommitted(CommittedEnvelope{MessageID: 1400, ChannelID: "room", ChannelType: 2, MessageScopedUIDs: []string{"u2"}})
	effect, ok := state.nextCommitEffect(key)
	if !ok {
		t.Fatalf("nextCommitEffect() ok = false, want true")
	}
	if !state.commitInflight {
		t.Fatalf("commitInflight = false, want reserved before unsent pending effect")
	}
	reactor.states[key] = state
	reactor.pendingCommit = []commitEffect{effect}
	reactor.startEffectWorkers()
	reactor.close()
	go reactor.run()

	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := reactor.wait(waitCtx); err != nil {
		t.Fatalf("reactor wait error = %v, want drained after releasing unsent pending commit", err)
	}
	if state.commitInflight {
		t.Fatalf("commitInflight = true after canceled unsent pending commit")
	}
}

type staticRecipientAuthorityResolverForCommitTest struct {
	nodeID uint64
}

func (r staticRecipientAuthorityResolverForCommitTest) ResolveRecipientAuthority(_ context.Context, _ string) (RecipientAuthorityTarget, error) {
	return authority.Target{HashSlot: 1, SlotID: 101, LeaderNodeID: r.nodeID, RouteRevision: 1001, AuthorityEpoch: 1}, nil
}

type blockingRecipientRouterForCommitTest struct {
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
	releaseO sync.Once
	mu       sync.Mutex
	calls    int
}

func newBlockingRecipientRouterForCommitTest() *blockingRecipientRouterForCommitTest {
	return &blockingRecipientRouterForCommitTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *blockingRecipientRouterForCommitTest) DispatchRecipientBatch(ctx context.Context, _ RecipientAuthorityTarget, _ RecipientBatch) error {
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

func (r *blockingRecipientRouterForCommitTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatalf("recipient dispatch did not start")
	}
}

func (r *blockingRecipientRouterForCommitTest) release() {
	r.releaseO.Do(func() {
		close(r.releaseC)
	})
}

func (r *blockingRecipientRouterForCommitTest) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type scriptedRecipientRouterForCommitTest struct {
	mu      sync.Mutex
	cond    *sync.Cond
	errs    []error
	batches []RecipientBatch
}

func (r *scriptedRecipientRouterForCommitTest) DispatchRecipientBatch(_ context.Context, _ RecipientAuthorityTarget, batch RecipientBatch) error {
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

func (r *scriptedRecipientRouterForCommitTest) waitCalls(t *testing.T, want int) {
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
			t.Fatalf("recipient dispatch calls = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *scriptedRecipientRouterForCommitTest) messageIDs() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint64, 0, len(r.batches))
	for _, batch := range r.batches {
		out = append(out, batch.Event.MessageID)
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
	key := channelKey(channelID)
	for _, reactor := range group.reactors {
		reactor.mu.Lock()
		state := reactor.states[key]
		if state != nil {
			backlog := state.commitBacklog()
			reactor.mu.Unlock()
			return backlog
		}
		reactor.mu.Unlock()
	}
	return 0
}
