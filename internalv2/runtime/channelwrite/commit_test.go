package channelwrite

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAppendSuccessEnqueuesCommittedEventsAndSendackCompletesBeforeRecipientEffects(t *testing.T) {
	router := newBlockingRecipientRouterForCommitTest()
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
	router.release()
}

type staticRecipientAuthorityResolverForCommitTest struct {
	nodeID uint64
}

func (r staticRecipientAuthorityResolverForCommitTest) ResolveRecipientAuthority(_ context.Context, _ string) (RecipientAuthorityTarget, error) {
	return RecipientAuthorityTarget{LeaderNodeID: r.nodeID}, nil
}

type blockingRecipientRouterForCommitTest struct {
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
}

func newBlockingRecipientRouterForCommitTest() *blockingRecipientRouterForCommitTest {
	return &blockingRecipientRouterForCommitTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *blockingRecipientRouterForCommitTest) DispatchRecipientBatch(_ context.Context, _ RecipientAuthorityTarget, _ RecipientBatch) error {
	r.once.Do(func() {
		close(r.started)
	})
	<-r.releaseC
	return nil
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
	close(r.releaseC)
}
