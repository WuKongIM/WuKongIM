package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestFollowerTickPullsFromLocalLEOPlusOne(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.LastPull().NextOffset == 1 }, time.Second, time.Millisecond)

	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Hour)})
		return net.LastAck().MatchOffset == 1
	}, time.Second, time.Millisecond)
	net.SetPullResponse(transport.PullResponse{})
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
		return net.LastPull().NextOffset == 2
	}, time.Second, time.Millisecond)
}

func TestFollowerPullInflightSuppressesDuplicatePull(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
	net.UnblockPulls()
}

func TestFollowerPullErrorBacksOff(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode:                   2,
		ReactorCount:                1,
		MailboxSize:                 16,
		Store:                       factory,
		Transport:                   net,
		ReplicationMinBackoff:       time.Hour,
		ReplicationMaxBackoff:       time.Hour,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
}

func TestFollowerStoreApplyResultSendsAck(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)

	fetch, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 99, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}})
	require.NoError(t, err)
	result, err := fetch.Await(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Fetch.Messages, 1)
	require.Equal(t, uint64(1), result.Fetch.CommittedSeq)
}

func TestFollowerAckResultResetsBackoff(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	net.SetAckError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net, ReplicationMinBackoff: time.Hour, ReplicationMaxBackoff: time.Hour})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.AckCalls() == 1 }, time.Second, time.Millisecond)
	net.SetAckError(nil)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.Equal(t, 1, net.AckCalls())
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Hour + time.Millisecond)}))
	require.Eventually(t, func() bool { return net.AckCalls() == 2 }, time.Second, time.Millisecond)
}

func TestStoreApplyPoolFullKeepsOnePendingPullAndRetries(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode:    2,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		Transport:    net,
		WorkerPools: worker.PoolsConfig{
			StoreApply: worker.PoolConfig{Name: "store-apply", Workers: 1, QueueSize: 1},
		},
	})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())

	factory.UnblockApplies()
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)
}

func TestFollowerStoreApplyErrorRetriesSamePendingPull(t *testing.T) {
	meta := followerTestMeta("apply-error-retry")
	state := replicationState{pendingPull: &transport.PullResponse{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, LeaderHW: 1, Records: []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}}}, applyOpID: 7}
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, ReplicationMinBackoff: time.Nanosecond, ReplicationMaxBackoff: time.Nanosecond})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication = state

	result := worker.Result{Kind: worker.TaskStoreApply, Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7}, Err: ch.ErrNotReady}
	r.handleStoreApplyResult(result)
	require.NotNil(t, rc.replication.pendingPull)
	require.Equal(t, uint64(1), rc.replication.pendingPull.Records[0].Index)
	require.Zero(t, rc.replication.applyOpID)
}

func TestAckPoolFullKeepsPendingAckAndRetriesOnTick(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode:             2,
		ReactorCount:          1,
		MailboxSize:           32,
		Store:                 factory,
		Transport:             net,
		ReplicationMinBackoff: time.Nanosecond,
		ReplicationMaxBackoff: time.Nanosecond,
		WorkerPools:           worker.PoolsConfig{RPC: worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}},
	})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)

	net.BlockPulls()
	blockerFence1 := ch.Fence{ChannelKey: "1:blocker", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 901}
	blockerFence2 := ch.Fence{ChannelKey: "1:blocker", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 902}
	require.NoError(t, g.pools.Submit(context.Background(), worker.Task{Kind: worker.TaskRPCPull, Fence: blockerFence1, RPCPull: &worker.RPCPullTask{Node: 1, Request: transport.PullRequest{ChannelKey: "1:blocker", NextOffset: 1}}}))
	require.NoError(t, g.pools.Submit(context.Background(), worker.Task{Kind: worker.TaskRPCPull, Fence: blockerFence2, RPCPull: &worker.RPCPullTask{Node: 1, Request: transport.PullRequest{ChannelKey: "1:blocker", NextOffset: 1}}}))

	factory.UnblockApplies()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.Equal(t, 0, net.AckCalls())

	net.UnblockPulls()
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Millisecond)})
		return net.LastAck().MatchOffset == 1
	}, time.Second, time.Millisecond)
}

func TestStaleRPCPullCompletionDoesNotClearNewerPullInflight(t *testing.T) {
	state := replicationState{pullInflight: true, pullOpID: 2}
	stale := worker.Result{Kind: worker.TaskRPCPull, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}, RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{ChannelKey: "1:a", Epoch: 1, LeaderEpoch: 1}}}
	applied := state.applyPullResult(stale, ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: state.pullOpID}, time.Unix(1, 0))
	require.False(t, applied)
	require.True(t, state.pullInflight)
	require.Equal(t, ch.OpID(2), state.pullOpID)
}

func TestStaleRPCAckCompletionDoesNotClearNewerAckInflight(t *testing.T) {
	state := replicationState{ackInflight: true, ackOpID: 4, ackMatch: 9}
	stale := worker.Result{Kind: worker.TaskRPCAck, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 3}}
	applied := state.applyAckResult(stale, ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: state.ackOpID}, time.Unix(1, 0))
	require.False(t, applied)
	require.True(t, state.ackInflight)
	require.Equal(t, ch.OpID(4), state.ackOpID)
	require.Equal(t, uint64(9), state.ackMatch)
}

func TestAckErrorRetryKeepsSameMatchOffset(t *testing.T) {
	state := replicationState{ackInflight: true, ackOpID: 5, ackMatch: 9}
	result := worker.Result{Kind: worker.TaskRPCAck, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 5}, Err: ch.ErrNotReady}
	applied := state.applyAckResult(result, result.Fence, time.Unix(1, 0))
	require.True(t, applied)
	require.False(t, state.ackInflight)
	require.True(t, state.pendingAck)
	require.Equal(t, uint64(9), state.pendingAckMatch)
}

func TestLeaderPullUsesStoreReadLogWorkerWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, appendFuture)

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 77,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	metaFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	_, err = metaFuture.Await(context.Background())
	require.NoError(t, err)

	factory.UnblockReadLogs()
	_, err = pullFuture.Await(context.Background())
	require.NoError(t, err)
}

func TestLeaderPullWaiterFailsOnMetadataFence(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-fence", ID: ch.ChannelID{ID: "pull-fence", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, OpID: 88, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024}})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))
	_, err = pullFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

func TestLeaderIgnoresAckAfterLeaderEpochBump(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	staleAck := Event{
		Kind: EventAck,
		Key:  meta.Key,
		Ack:  transport.AckRequest{ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, Follower: 2, MatchOffset: 100},
	}
	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, staleAck))

	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-current-ack"))
	require.NoError(t, err)
	requireFuturePending(t, future)
}

func appendQuorumEvent(meta ch.Meta, id uint64, payload string) Event {
	event := appendEvent(meta, id, payload)
	event.Append.CommitMode = ch.CommitModeQuorum
	return event
}

func followerTestMeta(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
}

type capturingTransport struct {
	mu        sync.Mutex
	pullCalls int
	ackCalls  int
	lastPull  transport.PullRequest
	lastAck   transport.AckRequest
	pullResp  transport.PullResponse
	pullErr   error
	ackErr    error
	blockPull chan struct{}
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{}
}

func (t *capturingTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pullCalls++
	t.lastPull = req
	block := t.blockPull
	resp := t.pullResp
	err := t.pullErr
	t.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return transport.PullResponse{}, ctx.Err()
		}
	}
	if err != nil {
		return transport.PullResponse{}, err
	}
	if resp.ChannelKey == "" {
		resp = transport.PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}
	}
	return resp, nil
}

func (t *capturingTransport) Ack(ctx context.Context, node ch.NodeID, req transport.AckRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackCalls++
	t.lastAck = req
	return t.ackErr
}

func (t *capturingTransport) LastPull() transport.PullRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPull
}

func (t *capturingTransport) LastAck() transport.AckRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastAck
}

func (t *capturingTransport) PullCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullCalls
}

func (t *capturingTransport) AckCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ackCalls
}

func (t *capturingTransport) SetPullResponse(resp transport.PullResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullResp = resp
}

func (t *capturingTransport) SetPullError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullErr = err
}

func (t *capturingTransport) SetAckError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackErr = err
}

func (t *capturingTransport) BlockPulls() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockPull = make(chan struct{})
}

func (t *capturingTransport) UnblockPulls() {
	t.mu.Lock()
	block := t.blockPull
	t.blockPull = nil
	t.mu.Unlock()
	if block != nil {
		close(block)
	}
}

type blockingApplyFactory struct {
	base         *store.MemoryFactory
	applyStarted chan struct{}
	unblock      chan struct{}
}

func newBlockingApplyFactory() *blockingApplyFactory {
	return &blockingApplyFactory{base: store.NewMemoryFactory(), applyStarted: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingApplyFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingApplyStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingApplyFactory) BlockApplies() {
	f.unblock = make(chan struct{})
}

func (f *blockingApplyFactory) ApplyStarted() bool {
	return len(f.applyStarted) > 0
}

func (f *blockingApplyFactory) UnblockApplies() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingApplyStore struct {
	store.ChannelStore
	parent *blockingApplyFactory
}

func (s *blockingApplyStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	select {
	case s.parent.applyStarted <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ApplyFollowerResult{}, ctx.Err()
	}
	return s.ChannelStore.ApplyFollower(ctx, req)
}

type blockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newBlockingReadLogFactory() *blockingReadLogFactory {
	return &blockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingReadLogFactory) ReadLogStarted() bool {
	return len(f.started) > 0
}

func (f *blockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingReadLogStore struct {
	store.ChannelStore
	parent *blockingReadLogFactory
}

func (s *blockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadLogResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadLog(ctx, req)
}
