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
	network.SetDropNotify(2, true)
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
	network.SetDropNotify(2, false)

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
