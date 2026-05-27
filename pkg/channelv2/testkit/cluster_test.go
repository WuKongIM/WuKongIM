package testkit

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeClusterCommitsWithMinISR2(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	res, err := h.Nodes[1].Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}

func TestLeaderAppendPullHintCommitsWithoutBackgroundTicks(t *testing.T) {
	network := transport.NewLocalNetwork()
	nodes := make(map[ch.NodeID]ch.Cluster)
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		cluster, err := service.New(service.Config{
			LocalNode:                   nodeID,
			Store:                       store.NewMemoryFactory(),
			ReactorCount:                1,
			Transport:                   network.Client(),
			ReplicationIdlePollInterval: time.Hour,
		})
		require.NoError(t, err)
		defer cluster.Close()
		nodes[nodeID] = cluster
		server, ok := cluster.(transport.Server)
		require.True(t, ok)
		network.Register(nodeID, server)
	}
	meta := ch.Meta{Key: ch.ChannelKey("1:pull-hint"), ID: ch.ChannelID{ID: "pull-hint", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	for _, node := range nodes {
		require.NoError(t, node.ApplyMeta(meta))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := nodes[1].Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
}

func TestPullHintLazyActivatesUnloadedFollowerAndReplicatesWithoutTicks(t *testing.T) {
	network := transport.NewLocalNetwork()
	meta := ch.Meta{Key: ch.ChannelKey("1:pull-hint-lazy-replication"), ID: ch.ChannelID{ID: "pull-hint-lazy-replication", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}

	leader, err := service.New(service.Config{
		LocalNode:                   1,
		Store:                       store.NewMemoryFactory(),
		ReactorCount:                1,
		Transport:                   network.Client(),
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, err)
	defer leader.Close()
	leaderServer, ok := leader.(transport.Server)
	require.True(t, ok)
	network.Register(1, leaderServer)

	resolver := &testkitCountingMetaResolver{meta: meta}
	follower, err := service.New(service.Config{
		LocalNode:                   2,
		Store:                       store.NewMemoryFactory(),
		ReactorCount:                1,
		Transport:                   network.Client(),
		MetaResolver:                resolver,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, err)
	defer follower.Close()
	followerServer, ok := follower.(transport.Server)
	require.True(t, ok)
	network.Register(2, followerServer)

	require.NoError(t, leader.ApplyMeta(meta))
	network.SetDropPullHint(2, true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan appendOutcome, 1)
	go func() {
		res, err := leader.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
		done <- appendOutcome{result: res, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case outcome := <-done:
			t.Fatalf("append completed before pull hint activation: result=%+v err=%v", outcome.result, outcome.err)
			return false
		default:
		}
		return network.DroppedPullHints(2) > 0
	}, time.Second, time.Millisecond)
	network.SetDropPullHint(2, false)

	require.NoError(t, network.PullHint(context.Background(), 2, transport.PullHintRequest{
		ChannelKey:      meta.Key,
		ChannelID:       meta.ID,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Leader:          meta.Leader,
		LeaderLEO:       1,
		ActivityVersion: 1,
		Reason:          transport.PullHintReasonAppend,
	}))

	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		require.Equal(t, uint64(1), outcome.result.MessageSeq)
	case <-time.After(time.Second):
		t.Fatal("append did not complete after pull hint lazy activation")
	}
	require.Equal(t, int32(1), resolver.calls.Load())
}

func TestThreeNodeClusterReactivatesStoppedFollowerWithPullHintAndCommits(t *testing.T) {
	network := transport.NewLocalNetwork()
	meta := ch.Meta{Key: ch.ChannelKey("1:reactivate-stopped-follower"), ID: ch.ChannelID{ID: "reactivate-stopped-follower", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	nodes := make(map[ch.NodeID]ch.Cluster)
	stores := make(map[ch.NodeID]*store.MemoryFactory)
	resolvers := make(map[ch.NodeID]*testkitCountingMetaResolver)
	observers := make(map[ch.NodeID]*testkitObserver)
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		resolver := &testkitCountingMetaResolver{meta: meta}
		factory := store.NewMemoryFactory()
		observer := &testkitObserver{}
		cluster, err := service.New(service.Config{
			LocalNode:                   nodeID,
			Store:                       factory,
			ReactorCount:                1,
			Transport:                   network.Client(),
			MetaResolver:                resolver,
			Observer:                    observer,
			ReplicationIdlePollInterval: time.Millisecond,
			IdleSlowdownAfter:           time.Millisecond,
			IdleEvictAfter:              5 * time.Millisecond,
			IdlePullMinInterval:         time.Millisecond,
			IdlePullMaxInterval:         time.Millisecond,
			IdleEvictCheckInterval:      time.Millisecond,
			PullHintRetryInterval:       time.Millisecond,
			AppendBatchMaxRecords:       1,
		})
		require.NoError(t, err)
		defer cluster.Close()
		nodes[nodeID] = cluster
		stores[nodeID] = factory
		resolvers[nodeID] = resolver
		observers[nodeID] = observer
		server, ok := cluster.(transport.Server)
		require.True(t, ok)
		network.Register(nodeID, server)
	}
	for _, node := range nodes {
		require.NoError(t, node.ApplyMeta(meta))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first, err := nodes[1].Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("before stop")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.MessageSeq)
	waitTestkitCommitted(t, nodes, stores, meta.ID, 2, 1, time.Second)
	waitTestkitCommitted(t, nodes, stores, meta.ID, 3, 1, time.Second)

	waitTestkitEvicted(t, nodes, observers, []ch.NodeID{2, 3}, 2*time.Second)
	beforeFollower2 := resolvers[2].calls.Load()
	beforeFollower3 := resolvers[3].calls.Load()

	require.NoError(t, nodes[1].ApplyMeta(meta))
	second, err := nodes[1].Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("after stop")}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.MessageSeq)
	waitTestkitCommitted(t, nodes, stores, meta.ID, 2, 2, time.Second)
	waitTestkitCommitted(t, nodes, stores, meta.ID, 3, 2, time.Second)
	require.Greater(t, resolvers[2].calls.Load(), beforeFollower2)
	require.Greater(t, resolvers[3].calls.Load(), beforeFollower3)
}

func TestThreeNodeClusterCatchesUpAfterTemporaryPullDrop(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	h.Network.SetDropPull(1, true)
	done := startAppend(t, h, 1, meta.ID, []byte("pull-drop"))
	requireDropObserved(t, h, done, func() int { return h.Network.DroppedPulls(1) }, time.Second)

	h.Network.SetDropPull(1, false)
	res := waitAppendResult(t, h, done, time.Second)
	require.Equal(t, uint64(1), res.MessageSeq)
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}

func TestThreeNodeClusterCatchesUpAfterTemporaryAckDrop(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	h.Network.SetDropAck(1, true)
	done := startAppend(t, h, 1, meta.ID, []byte("ack-drop"))
	requireDropObserved(t, h, done, func() int { return h.Network.DroppedAcks(1) }, time.Second)

	h.Network.SetDropAck(1, false)
	res := waitAppendResult(t, h, done, time.Second)
	require.Equal(t, uint64(1), res.MessageSeq)
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}

type appendOutcome struct {
	result ch.AppendResult
	err    error
}

type testkitCountingMetaResolver struct {
	meta  ch.Meta
	calls atomic.Int32
}

func (r *testkitCountingMetaResolver) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if id != r.meta.ID {
		return ch.Meta{}, ch.ErrChannelNotFound
	}
	r.calls.Add(1)
	return r.meta, nil
}

type testkitObserver struct {
	evicted atomic.Int32
}

func (o *testkitObserver) SetReactorMailboxDepth(int, string, int) {}
func (o *testkitObserver) SetWorkerQueueDepth(string, int)         {}
func (o *testkitObserver) ObserveAppendBatch(int, int, time.Duration) {
}
func (o *testkitObserver) ObserveAppendLatency(ch.CommitMode, time.Duration) {}
func (o *testkitObserver) ObserveWorkerResult(worker.TaskKind, error, time.Duration) {
}
func (o *testkitObserver) ObserveChannelRuntimeLoaded(ch.ChannelKey) {}
func (o *testkitObserver) ObserveChannelRuntimeEvicted(ch.ChannelKey, ch.Role) {
	o.evicted.Add(1)
}
func (o *testkitObserver) ObservePullHintSent(ch.ChannelKey, ch.NodeID, transport.PullHintReason) {
}
func (o *testkitObserver) ObservePullHintDropped(ch.ChannelKey, ch.NodeID, error) {}

func startAppend(t testing.TB, h *ClusterHarness, nodeID ch.NodeID, id ch.ChannelID, payload []byte) <-chan appendOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	done := make(chan appendOutcome, 1)
	go func() {
		res, err := h.Nodes[nodeID].Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{Payload: payload}})
		done <- appendOutcome{result: res, err: err}
	}()
	return done
}

func requireDropObserved(t testing.TB, h *ClusterHarness, done <-chan appendOutcome, drops func() int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		require.NoError(t, h.TickAll(context.Background()))
		select {
		case outcome := <-done:
			t.Fatalf("append completed before a replication RPC was dropped: result=%+v err=%v", outcome.result, outcome.err)
		default:
		}
		if drops() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("replication RPC was not dropped within %s", timeout)
}

func waitAppendResult(t testing.TB, h *ClusterHarness, done <-chan appendOutcome, timeout time.Duration) ch.AppendResult {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		require.NoError(t, h.TickAll(context.Background()))
		select {
		case outcome := <-done:
			require.NoError(t, outcome.err)
			return outcome.result
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("append did not complete within %s", timeout)
	return ch.AppendResult{}
}

func waitTestkitCommitted(t testing.TB, nodes map[ch.NodeID]ch.Cluster, stores map[ch.NodeID]*store.MemoryFactory, id ch.ChannelID, nodeID ch.NodeID, seq uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tickTestkitNodes(context.Background(), nodes)
		messages, err := readTestkitStore(stores[nodeID], id, seq)
		if err == nil && len(messages) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not commit seq %d", nodeID, seq)
}

func waitTestkitEvicted(t testing.TB, nodes map[ch.NodeID]ch.Cluster, observers map[ch.NodeID]*testkitObserver, nodeIDs []ch.NodeID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tickTestkitNodes(context.Background(), nodes)
		allEvicted := true
		for _, nodeID := range nodeIDs {
			if observers[nodeID].evicted.Load() == 0 {
				allEvicted = false
				break
			}
		}
		if allEvicted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("nodes %v did not evict channel runtime", nodeIDs)
}

func readTestkitStore(factory *store.MemoryFactory, id ch.ChannelID, seq uint64) ([]ch.Message, error) {
	if factory == nil {
		return nil, ch.ErrChannelNotFound
	}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return nil, err
	}
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: seq, MaxSeq: seq, Limit: 1, MaxBytes: 1024})
	if err != nil {
		return nil, err
	}
	return read.Messages, nil
}

func tickTestkitNodes(ctx context.Context, nodes map[ch.NodeID]ch.Cluster) {
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		if node := nodes[nodeID]; node != nil {
			_ = node.Tick(ctx)
		}
	}
}
