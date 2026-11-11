//go:build integration

package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// After promise activation a write-fenced replica replacement writes no
// barrier, so the learner must still receive the committed prefix (and its
// commit) through learner repair alone, within the migration fence lifetime.
func TestPromiseLearnerCatchesUpUnderWriteFence(t *testing.T) {
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	runtimes := make(map[ch.NodeID]*Runtime)
	stores := make(map[ch.NodeID]ReplicaStore)
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		factory := channelstore.NewMessageDBFactory(t.TempDir())
		store, err := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes})
		require.NoError(t, err)
		runtime, err := NewRuntime(RuntimeConfig{LocalNode: node, Store: store, Link: runtimeTestLink{from: node, router: router}, PromiseActive: func() bool { return true }})
		require.NoError(t, err)
		runtimes[node], stores[node] = runtime, store
		router.register(node, runtime.ExchangeServer())
		t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())); require.NoError(t, factory.Close()) })
	}
	a := Authority{Key: "2:promise-learner", ChannelID: ch.ChannelID{ID: "promise-learner", Type: 2}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	ctx := context.Background()
	_, err := runtimes[1].Log().Install(ctx, a)
	require.NoError(t, err)
	_, err = runtimes[1].Log().Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: 1}, Records: []ch.Record{{ID: 101, Epoch: 1, FromUID: "sender", ClientMsgNo: "promise", Payload: []byte("promise"), SizeBytes: 7, ServerTimestampMS: 111}}})
	require.NoError(t, err)
	// Membership change adds the learner, then the replacement fence follows.
	a.ID.ChannelEpoch++
	a.ID.FenceVersion++
	a.Learners = []ch.NodeID{4}
	installed, err := runtimes[1].Log().Install(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(1), installed.HW)
	a.ID.FenceVersion++
	a.WriteFence = ch.WriteFence{Token: "replacement", Version: 1, Reason: ch.WriteFenceReasonReplicaReplace}
	installed, err = runtimes[1].Log().Install(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(1), installed.HW, "a fenced promise install consumes no log index")
	require.Eventually(t, func() bool {
		loaded, err := stores[4].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
		return err == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil && loaded.Items[0].State.LEO == 1 && loaded.Items[0].State.Committed == 1
	}, 2*time.Second, time.Millisecond, "learner never received the committed prefix")
}
