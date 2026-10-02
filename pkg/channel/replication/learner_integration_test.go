//go:build integration

package replication

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// A replacement learner must receive existing exact proposals even while the
// task fences business writes; its durable HW must include the recovery barrier.
func TestNativeLearnerCatchesUpUnderWriteFence(t *testing.T) {
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	runtimes := make(map[ch.NodeID]*Runtime)
	stores := make(map[ch.NodeID]ReplicaStore)
	factories := make(map[ch.NodeID]*channelstore.MessageDBFactory)
	var corrupt atomic.Bool
	var probes atomic.Int32
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		factory := channelstore.NewMessageDBFactory(t.TempDir())
		factories[node] = factory
		store, err := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes})
		require.NoError(t, err)
		runtime, err := NewRuntime(RuntimeConfig{LocalNode: node, Store: store, Link: learnerProofLink{base: runtimeTestLink{from: node, router: router}, corrupt: &corrupt, probes: &probes}})
		require.NoError(t, err)
		runtimes[node], stores[node] = runtime, store
		router.register(node, runtime.ExchangeServer())
		t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())); require.NoError(t, factory.Close()) })
	}
	a := Authority{Key: "2:native-learner", ChannelID: ch.ChannelID{ID: "native-learner", Type: 2}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	ctx := context.Background()
	_, err := runtimes[1].Log().Install(ctx, a)
	require.NoError(t, err)
	_, err = runtimes[1].Log().Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: 1}, Records: []ch.Record{{ID: 101, Epoch: 1, FromUID: "sender", ClientMsgNo: "native", Payload: []byte("native"), SizeBytes: 6, ServerTimestampMS: 111}}})
	require.NoError(t, err)
	a.ID.ChannelEpoch++
	a.ID.FenceVersion++
	a.Learners = []ch.NodeID{4}
	a.WriteFence = ch.WriteFence{Token: "replacement", Version: 2, Reason: ch.WriteFenceReasonReplicaReplace}
	router.register(4, nil)
	installed := installLearnerAuthorityEventually(t, runtimes[1].Log(), a)
	require.Greater(t, installed.HW, uint64(1))
	router.register(4, runtimes[4].ExchangeServer())
	require.Eventually(t, func() bool {
		loaded, err := stores[4].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
		return err == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil && loaded.Items[0].State.LEO == installed.HW && loaded.Items[0].State.Committed == installed.HW
	}, 2*time.Second, time.Millisecond, "non-voter learner never received the native committed prefix")
	require.NoError(t, runtimes[1].Log().(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, a))
	require.Eventually(t, func() bool {
		for _, voter := range []ch.NodeID{2, 3} {
			loaded, e := stores[voter].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
			if e != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.Committed != installed.HW {
				return false
			}
		}
		return true
	}, 2*time.Second, time.Millisecond)
	// The leader may retire original bodies after every replica caught up.
	// A later authority must not restart that learner at an unavailable body.
	handle, err := factories[1].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	_, err = handle.AdoptRetentionBoundary(ctx, 1, ch.RetentionCursorCommitted)
	require.NoError(t, err)
	trim, err := handle.TrimMessagesThrough(ctx, 1, channelstore.RetentionTrimOptions{MaxMessages: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, 1, trim.Deleted)
	require.NoError(t, handle.Close())
	// A new authority must resume from an independently verified matching tail.
	a.WriteFence = ch.WriteFence{}
	a.ID.FenceVersion++
	corrupt.Store(true)
	advanced := installLearnerAuthorityEventually(t, runtimes[1].Log(), a)
	require.Eventually(t, func() bool { return probes.Load() >= 3 }, 2*time.Second, time.Millisecond, "pruned repair must probe the learner instead of repeatedly fetching removed bodies")
	unchanged, err := stores[4].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
	require.NoError(t, err)
	require.NoError(t, unchanged.Items[0].Err)
	require.Equal(t, installed.HW, unchanged.Items[0].State.Committed, "a structurally valid but unmatched tail must not advance repair")
	corrupt.Store(false)
	require.Eventually(t, func() bool {
		loaded, e := stores[4].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
		return e == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil && loaded.Items[0].State.Committed == advanced.HW
	}, 2*time.Second, time.Millisecond, "matching learner tail must resume after the pruned prefix")
	// With the learner up but both remote voters down, no business success is
	// possible. Restoring voters permits an exact retry of the pending proposal.
	router.register(2, nil)
	router.register(3, nil)
	proposal := Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: 2}, Records: []ch.Record{{ID: 102, Epoch: a.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: "native-next", Payload: []byte("next"), SizeBytes: 4, ServerTimestampMS: 112}}}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = runtimes[1].Log().Commit(bounded, proposal)
	cancel()
	require.Error(t, err, "a learner must never replace a missing voter")
	router.register(2, runtimes[2].ExchangeServer())
	router.register(3, runtimes[3].ExchangeServer())
	receipt, err := runtimes[1].Log().Commit(ctx, proposal)
	require.NoError(t, err)
	require.Greater(t, receipt.HW, installed.HW)
	require.Eventually(t, func() bool {
		loaded, err := stores[4].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
		return err == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil && loaded.Items[0].State.Committed == receipt.HW
	}, 2*time.Second, time.Millisecond, "learner must follow new commits after its initial copy")
	t.Log("native_learner_pruned_repair: real_disk=true exact_tail_required=true corrupt_tail_rejected=true new_authority=true later_commits=true learner_never_votes=true")
}

type learnerProofLink struct {
	base    runtimeTestLink
	corrupt *atomic.Bool
	probes  *atomic.Int32
}

func (l learnerProofLink) Exchange(ctx context.Context, target ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	r, err := l.base.Exchange(ctx, target, batch)
	if err == nil && target == 4 && l.corrupt.Load() {
		for i, item := range batch.Items {
			if item.Kind == ExchangeProbe && r.Items[i].Probe.State.LEO > 0 {
				r.Items[i].Probe.State.Manifest.Digest[0]++
				r.Items[i].Probe.State.TailIdentity.Digest[0]++
				l.probes.Add(1)
			}
		}
	}
	return r, err
}

// Trailing voter writes and checkpoints remain concurrent with a new fence.
// An unstable read proof or incomplete convergence must be retried, just as the native
// migration executor retries its phase. All other errors remain test failures.
func installLearnerAuthorityEventually(t *testing.T, log DurableQuorumLog, authority Authority) Installed {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		installed, err := log.Install(ctx, authority)
		if (errors.Is(err, errRecoveryProbeIncomplete) || errors.Is(err, ch.ErrNotReady)) && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
			continue
		}
		require.NoError(t, err, "authority never obtained a stable exact proof")
		return installed
	}
}
