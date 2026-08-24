package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestQuorumLeaderActivationAndAppendBypassPullAckHotPath(t *testing.T) {
	log := &reactorCaptureQuorumLog{}
	observer := &captureObserver{}
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 32, Store: store.NewMemoryFactory(),
		QuorumLog: log, Observer: observer, AppendBatchMaxRecords: 2, AppendBatchMaxWait: time.Hour,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-direct", 1, 1)
	meta.RouteGeneration = 7
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	meta.MinISR = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	installed := log.authorities()
	require.Len(t, installed, 1)
	require.Equal(t, replication.Authority{
		Key: meta.Key, ChannelID: meta.ID,
		ID:     replication.AuthorityID{ChannelEpoch: meta.Epoch, LeaderTerm: meta.LeaderEpoch, FenceVersion: meta.RouteGeneration},
		Leader: meta.Leader, Voters: meta.ISR, WriteQuorum: meta.MinISR,
	}, installed[0])

	firstEvent := appendEvent(meta, 11, "a")
	firstEvent.Append.CommitMode = ch.CommitModeQuorum
	firstEvent.Append.ServerAllocatedMessageIDs = true
	first, err := g.Submit(context.Background(), meta.Key, firstEvent)
	require.NoError(t, err)
	secondEvent := appendEvent(meta, 12, "b")
	secondEvent.Append.CommitMode = ch.CommitModeQuorum
	secondEvent.Append.ServerAllocatedMessageIDs = true
	second, err := g.Submit(context.Background(), meta.Key, secondEvent)
	require.NoError(t, err)

	firstResult := awaitFutureResult(t, first)
	secondResult := awaitFutureResult(t, second)
	require.NoError(t, firstResult.Err)
	require.NoError(t, secondResult.Err)
	require.Equal(t, uint64(1), firstResult.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, uint64(2), secondResult.AppendBatch.Items[0].MessageSeq)

	proposals := log.proposals()
	require.Len(t, proposals, 2, "independent caller retries must not inherit a transient reactor batch boundary")
	require.NotEqual(t, ch.CommandID{}, proposals[0].CommandID)
	require.NotEqual(t, proposals[0].CommandID, proposals[1].CommandID)
	require.Equal(t, uint64(11), proposals[0].Records[0].ID)
	require.Equal(t, meta.Epoch, proposals[0].Records[0].Epoch)
	require.Equal(t, uint64(12), proposals[1].Records[0].ID)
	require.True(t, proposals[0].ServerAllocatedMessageIDs)
	require.True(t, proposals[1].ServerAllocatedMessageIDs)
	require.Zero(t, observer.PullHintsSent(), "quorum receipt must not use PullHint/AckOffset to complete SENDACK")
}

func TestQuorumLeaderFlushesOneProposalWithoutReactorBatchWait(t *testing.T) {
	log := &reactorCaptureQuorumLog{}
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 32, Store: store.NewMemoryFactory(),
		QuorumLog: log, AppendBatchMaxRecords: 128, AppendBatchMaxWait: time.Hour,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-immediate", 1, 1)
	meta.RouteGeneration = 7
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	meta.MinISR = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	event := appendEvent(meta, 11, "a")
	event.Append.CommitMode = ch.CommitModeQuorum
	future, err := g.Submit(context.Background(), meta.Key, event)
	require.NoError(t, err)
	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, uint64(1), result.AppendBatch.Items[0].MessageSeq)
	require.Len(t, log.proposals(), 1)
}

func TestQuorumFollowerDoesNotScheduleLegacyPullAckReplication(t *testing.T) {
	log := &reactorCaptureQuorumLog{}
	transport := newCapturingTransport()
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		Transport: transport, QuorumLog: log,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-follower-no-pull", 1, 2)
	meta.RouteGeneration = 1
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	for i := 0; i < 4; i++ {
		require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Duration(i+1) * time.Hour)}))
	}
	require.Zero(t, transport.PullCalls())
	require.Zero(t, transport.AckCalls())
}

func TestQuorumLeaderIdleEvictionDoesNotWaitForLegacyFollowerStopACKs(t *testing.T) {
	log := &reactorCaptureQuorumLog{}
	observer := &captureObserver{}
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		QuorumLog: log, Observer: observer,
		IdleEvictAfter: 5 * time.Millisecond, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-idle-evict", 1, 1)
	meta.RouteGeneration = 1
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	meta.MinISR = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.Eventually(t, func() bool {
		return observer.RuntimeEvicted() == 1
	}, 250*time.Millisecond, time.Millisecond)
	releases := log.releasedAuthorities()
	require.Equal(t, []releasedQuorumAuthority{{key: meta.Key, authority: replication.AuthorityID{
		ChannelEpoch: meta.Epoch, LeaderTerm: meta.LeaderEpoch, FenceVersion: meta.RouteGeneration,
	}}}, releases)
}

func TestQuorumLeaderActivationFailsClosedWhenInstallFails(t *testing.T) {
	log := &reactorCaptureQuorumLog{installErr: ch.ErrNotReady}
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		QuorumLog: log, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-install-fails", 1, 1)
	meta.RouteGeneration = 3
	err = awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.ErrorIs(t, err, ch.ErrNotReady)

	event := appendEvent(meta, 1, "a")
	event.Append.CommitMode = ch.CommitModeQuorum
	future, err := g.Submit(context.Background(), meta.Key, event)
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrNotReady)
}

func TestQuorumLeaderReinstallsWhenRouteGenerationChanges(t *testing.T) {
	log := &reactorCaptureQuorumLog{}
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		QuorumLog: log, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-route-fence", 1, 1)
	meta.RouteGeneration = 4
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	meta.RouteGeneration++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	installed := log.authorities()
	require.Len(t, installed, 2)
	require.Equal(t, uint64(4), installed[0].ID.FenceVersion)
	require.Equal(t, uint64(5), installed[1].ID.FenceVersion)
}

func TestQuorumInstallBlocksEvictionUntilAuthorityBarrierCompletes(t *testing.T) {
	log := newBlockingReactorQuorumLog()
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		QuorumLog: log, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("quorum-install-pending", 1, 1)
	meta.RouteGeneration = 9
	future, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	select {
	case <-log.started:
	case <-time.After(time.Second):
		t.Fatal("quorum install did not start")
	}

	result, err := g.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Equal(t, 0, result.Evicted)
	require.Equal(t, 1, result.SkippedBusy)

	close(log.release)
	require.NoError(t, awaitFutureResult(t, future).Err)
}

func TestQuorumInstallWaiterFailsWhenReactorCloses(t *testing.T) {
	log := newBlockingReactorQuorumLog()
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(),
		QuorumLog: log, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, err)

	meta := testMeta("quorum-install-close", 1, 1)
	meta.RouteGeneration = 11
	future, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	select {
	case <-log.started:
	case <-time.After(time.Second):
		t.Fatal("quorum install did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- g.Close() }()
	select {
	case <-future.Done():
		require.ErrorIs(t, future.Result().Err, ch.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("reactor close did not fail the quorum install waiter")
	}
	close(log.release)
	require.NoError(t, <-closeDone)
}

type blockingReactorQuorumLog struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingReactorQuorumLog() *blockingReactorQuorumLog {
	return &blockingReactorQuorumLog{started: make(chan struct{}), release: make(chan struct{})}
}

func (l *blockingReactorQuorumLog) Install(ctx context.Context, authority replication.Authority) (replication.Installed, error) {
	l.once.Do(func() { close(l.started) })
	select {
	case <-ctx.Done():
		return replication.Installed{}, ctx.Err()
	case <-l.release:
		return replication.Installed{Authority: authority.ID}, nil
	}
}

func (l *blockingReactorQuorumLog) Commit(context.Context, replication.Proposal) (replication.Receipt, error) {
	return replication.Receipt{}, ch.ErrNotReady
}

func (l *blockingReactorQuorumLog) Release(ch.ChannelKey, replication.AuthorityID) bool {
	return true
}

type releasedQuorumAuthority struct {
	key       ch.ChannelKey
	authority replication.AuthorityID
}

type reactorCaptureQuorumLog struct {
	mu         sync.Mutex
	installed  []replication.Authority
	committed  []replication.Proposal
	released   []releasedQuorumAuthority
	installErr error
	leo        uint64
}

func (l *reactorCaptureQuorumLog) Install(_ context.Context, authority replication.Authority) (replication.Installed, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.installed = append(l.installed, authority)
	if l.installErr != nil {
		return replication.Installed{}, l.installErr
	}
	return replication.Installed{Authority: authority.ID, LEO: l.leo, HW: l.leo}, nil
}

func (l *reactorCaptureQuorumLog) Commit(_ context.Context, proposal replication.Proposal) (replication.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.committed = append(l.committed, proposal)
	first := l.leo + 1
	l.leo += uint64(len(proposal.Records))
	return replication.Receipt{Authority: proposal.Expected, CommandID: proposal.CommandID, First: first, Last: l.leo, HW: l.leo}, nil
}

func (l *reactorCaptureQuorumLog) Release(key ch.ChannelKey, authority replication.AuthorityID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released = append(l.released, releasedQuorumAuthority{key: key, authority: authority})
	return true
}

func (l *reactorCaptureQuorumLog) authorities() []replication.Authority {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]replication.Authority(nil), l.installed...)
}

func (l *reactorCaptureQuorumLog) proposals() []replication.Proposal {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]replication.Proposal(nil), l.committed...)
}

func (l *reactorCaptureQuorumLog) releasedAuthorities() []releasedQuorumAuthority {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]releasedQuorumAuthority(nil), l.released...)
}
