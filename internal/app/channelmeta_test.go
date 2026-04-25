package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestChannelMetaSyncRefreshProjectsLeaderEpochLeaseAndApply(t *testing.T) {
	leaseUntil := time.UnixMilli(1_700_000_123_000).UTC()
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    "u1",
		ChannelType:  1,
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatU64),
		LeaseUntilMS: leaseUntil.UnixMilli(),
	}

	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			{ID: "u1", Type: 1}: meta,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), channel.ChannelID{ID: "u1", Type: 1})
	require.NoError(t, err)
	require.Equal(t, channel.Meta{
		Key:         channelhandler.KeyFromChannelID(channel.ChannelID{ID: "u1", Type: 1}),
		ID:          channel.ChannelID{ID: "u1", Type: 1},
		Epoch:       5,
		LeaderEpoch: 7,
		Replicas:    []channel.NodeID{2, 3},
		ISR:         []channel.NodeID{2, 3},
		Leader:      2,
		MinISR:      2,
		LeaseUntil:  leaseUntil,
		Status:      channel.StatusActive,
		Features: channel.Features{
			MessageSeqFormat: channel.MessageSeqFormatU64,
		},
	}, got)
	require.Equal(t, []channel.Meta{got}, cluster.applied)
	require.Equal(t, map[channel.ChannelKey]struct{}{got.Key: {}}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncNeedsLeaderRepair(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    "repair",
		ChannelType:  1,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       3,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}

	t.Run("cache miss", func(t *testing.T) {
		syncer := &channelMetaSync{}
		need, reason := syncer.needsLeaderRepair(meta)
		require.False(t, need)
		require.Empty(t, reason)
	})

	t.Run("alive", func(t *testing.T) {
		syncer := &channelMetaSync{}
		syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusAlive)
		need, reason := syncer.needsLeaderRepair(meta)
		require.False(t, need)
		require.Empty(t, reason)
	})

	t.Run("suspect", func(t *testing.T) {
		syncer := &channelMetaSync{}
		syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusSuspect)
		need, reason := syncer.needsLeaderRepair(meta)
		require.False(t, need)
		require.Empty(t, reason)
	})

	t.Run("dead", func(t *testing.T) {
		syncer := &channelMetaSync{}
		syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDead)
		need, reason := syncer.needsLeaderRepair(meta)
		require.True(t, need)
		require.Equal(t, "leader_dead", reason)
	})

	t.Run("draining", func(t *testing.T) {
		syncer := &channelMetaSync{}
		syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDraining)
		need, reason := syncer.needsLeaderRepair(meta)
		require.True(t, need)
		require.Equal(t, "leader_draining", reason)
	})

	t.Run("expired lease", func(t *testing.T) {
		syncer := &channelMetaSync{}
		candidate := meta
		candidate.LeaseUntilMS = time.Now().Add(-time.Second).UnixMilli()
		need, reason := syncer.needsLeaderRepair(candidate)
		require.True(t, need)
		require.Equal(t, "leader_lease_expired", reason)
	})

	t.Run("missing leader", func(t *testing.T) {
		syncer := &channelMetaSync{}
		candidate := meta
		candidate.Leader = 0
		need, reason := syncer.needsLeaderRepair(candidate)
		require.True(t, need)
		require.Equal(t, "leader_missing", reason)
	})

	t.Run("leader not in replicas", func(t *testing.T) {
		syncer := &channelMetaSync{}
		candidate := meta
		candidate.Leader = 9
		need, reason := syncer.needsLeaderRepair(candidate)
		require.True(t, need)
		require.Equal(t, "leader_not_replica", reason)
	})
}

func TestChannelMetaSyncRefreshWarmsNodeLivenessOnCacheMiss(t *testing.T) {
	id := channel.ChannelID{ID: "strict-liveness", Type: 1}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 6,
		LeaderEpoch:  8,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	repaired := authoritative
	repaired.Leader = 2
	repaired.LeaderEpoch++
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: authoritative,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	repairer := &stubChannelMetaRepairer{
		meta:    repaired,
		changed: true,
	}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		repairer:  repairer,
		localNode: 2,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{
				slotID: 9,
				peers:  []uint64{2, 3},
				leader: 2,
				nodes: []controllermeta.ClusterNode{
					{NodeID: 2, Status: controllermeta.NodeStatusAlive},
					{NodeID: 3, Status: controllermeta.NodeStatusDead},
				},
			},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_dead", repairer.calls[0].reason)
	status, ok := syncer.nodeLivenessStatus(3)
	require.True(t, ok)
	require.Equal(t, controllermeta.NodeStatusDead, status)
	require.Equal(t, channel.NodeID(2), got.Leader)
	require.Equal(t, []channel.Meta{got}, cluster.applied)
}

func TestChannelMetaSyncRefreshRepairsObservedRuntimeLeaderDrift(t *testing.T) {
	id := channel.ChannelID{ID: "leader-drift", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	repaired := authoritative
	repaired.Leader = 2
	repaired.LeaderEpoch++
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: authoritative,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	repairer := &stubChannelMetaRepairer{
		meta:    repaired,
		changed: true,
	}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		repairer:  repairer,
		localNode: 2,
		localRuntime: fakeChannelHandlerRuntime{
			handles: map[channel.ChannelKey]channel.HandlerChannel{
				key: fakeHandlerChannel{
					key:  key,
					meta: projectChannelMeta(authoritative),
					state: channel.ReplicaState{
						ChannelKey:  key,
						Epoch:       authoritative.ChannelEpoch,
						Leader:      2,
						Role:        channel.ReplicaRoleLeader,
						CommitReady: true,
						HW:          9,
						LEO:         9,
					},
				},
			},
		},
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_drift", repairer.calls[0].reason)
	require.Equal(t, channel.NodeID(2), got.Leader)
	require.Equal(t, []channel.Meta{got}, cluster.applied)
}

func TestChannelMetaSyncUpdateNodeLivenessRefreshesAffectedSlotWhenLeaderBecomesDead(t *testing.T) {
	id := channel.ChannelID{ID: "dead-leader-event", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 6,
		LeaderEpoch:  8,
		Replicas:     []uint64{2, 7},
		ISR:          []uint64{2, 7},
		Leader:       7,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	repaired := authoritative
	repaired.Leader = 2
	repaired.LeaderEpoch++
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: authoritative,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	repairer := &stubChannelMetaRepairer{
		meta:    repaired,
		changed: true,
	}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		repairer:        repairer,
		localNode:       2,
		refreshInterval: 0,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 3, peers: []uint64{2, 7}, leader: 2},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
		localRuntime: fakeChannelHandlerRuntime{
			handles: map[channel.ChannelKey]channel.HandlerChannel{
				key: fakeHandlerChannel{
					key:  key,
					meta: projectChannelMeta(authoritative),
					state: channel.ReplicaState{
						ChannelKey:  key,
						Epoch:       authoritative.ChannelEpoch,
						Leader:      channel.NodeID(authoritative.Leader),
						Role:        channel.ReplicaRoleFollower,
						CommitReady: true,
						HW:          9,
						LEO:         9,
					},
				},
			},
		},
	}
	syncer.mu.Lock()
	syncer.trackAppliedLocalKeyLocked(key)
	syncer.mu.Unlock()

	require.NoError(t, syncer.Start())
	syncer.UpdateNodeLiveness(7, controllermeta.NodeStatusDead)

	require.Eventually(t, func() bool {
		source.mu.Lock()
		getCalls := source.getCalls
		source.mu.Unlock()

		cluster.mu.Lock()
		defer cluster.mu.Unlock()
		if getCalls == 0 || len(cluster.runtimeUpserts) == 0 {
			return false
		}
		return cluster.runtimeUpserts[len(cluster.runtimeUpserts)-1].Leader == 2
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, syncer.Stop())
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_dead", repairer.calls[0].reason)
}

func TestChannelMetaSyncApplyAuthoritativeMetaSchedulesRefreshWhenLeaderAlreadyKnownDead(t *testing.T) {
	id := channel.ChannelID{ID: "dead-before-apply", Type: 1}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 7,
		LeaderEpoch:  9,
		Replicas:     []uint64{2, 7},
		ISR:          []uint64{2, 7},
		Leader:       7,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	repaired := authoritative
	repaired.Leader = 2
	repaired.LeaderEpoch++
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: authoritative,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	repairer := &stubChannelMetaRepairer{
		meta:    repaired,
		changed: true,
	}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		repairer:        repairer,
		localNode:       2,
		refreshInterval: 0,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 3, peers: []uint64{2, 7}, leader: 2},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
	}

	require.NoError(t, syncer.Start())
	syncer.UpdateNodeLiveness(7, controllermeta.NodeStatusDead)

	_, err := syncer.applyAuthoritativeMeta(authoritative)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		cluster.mu.Lock()
		defer cluster.mu.Unlock()
		if len(cluster.runtimeUpserts) < 2 {
			return false
		}
		return cluster.runtimeUpserts[len(cluster.runtimeUpserts)-1].Leader == 2
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, syncer.Stop())
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_dead", repairer.calls[0].reason)
}

func TestProjectChannelMetaKeepsZeroLeaseUnset(t *testing.T) {
	meta := projectChannelMeta(metadb.ChannelRuntimeMeta{
		ChannelID:   "u0",
		ChannelType: 1,
		LeaderEpoch: 3,
		Leader:      2,
		Replicas:    []uint64{2, 3},
		ISR:         []uint64{2, 3},
		MinISR:      2,
	})

	require.True(t, meta.LeaseUntil.IsZero())
	require.Equal(t, channelhandler.KeyFromChannelID(channel.ChannelID{ID: "u0", Type: 1}), meta.Key)
}

func TestChannelMetaSyncSyncOnceAppliesOnlyLocalReplicaMetas(t *testing.T) {
	source := &fakeChannelMetaSource{
		list: []metadb.ChannelRuntimeMeta{
			{
				ChannelID:    "local",
				ChannelType:  1,
				ChannelEpoch: 1,
				LeaderEpoch:  2,
				Replicas:     []uint64{2, 3},
				ISR:          []uint64{2, 3},
				Leader:       2,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
			},
			{
				ChannelID:    "remote",
				ChannelType:  1,
				ChannelEpoch: 3,
				LeaderEpoch:  4,
				Replicas:     []uint64{7, 8},
				ISR:          []uint64{7, 8},
				Leader:       7,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
	}

	require.NoError(t, syncer.syncOnce(context.Background()))
	require.Len(t, cluster.applied, 1)
	require.Equal(t, channel.ChannelID{ID: "local", Type: 1}, cluster.applied[0].ID)
	require.Equal(t, map[channel.ChannelKey]struct{}{
		cluster.applied[0].Key: {},
	}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncRefreshCachesNonLocalReplicaMeta(t *testing.T) {
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			{ID: "remote", Type: 1}: {
				ChannelID:    "remote",
				ChannelType:  1,
				ChannelEpoch: 2,
				LeaderEpoch:  3,
				Replicas:     []uint64{7, 8},
				ISR:          []uint64{7, 8},
				Leader:       7,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), channel.ChannelID{ID: "remote", Type: 1})
	require.NoError(t, err)
	require.Equal(t, channel.ChannelID{ID: "remote", Type: 1}, got.ID)
	require.Len(t, cluster.applied, 1)
	require.Empty(t, cluster.runtimeUpserts)
	require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncRefreshCachesRemoteRoutingMetaWithoutRuntime(t *testing.T) {
	id := channel.ChannelID{ID: "remote", Type: 1}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 2,
				LeaderEpoch:  3,
				Replicas:     []uint64{7, 8},
				ISR:          []uint64{7, 8},
				Leader:       7,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, []channel.Meta{got}, cluster.routingApplied)
	require.Empty(t, cluster.runtimeUpserts)
	require.Empty(t, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncStartDoesNotScanAuthoritativeMetas(t *testing.T) {
	source := &fakeChannelMetaSource{}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		localNode:       2,
		refreshInterval: time.Millisecond,
	}

	require.NoError(t, syncer.Start())
	require.NoError(t, syncer.Stop())
	require.Equal(t, 0, source.listCalls)
	require.Empty(t, cluster.applied)
}

func TestChannelMetaSyncActivateByKeyUsesAuthoritativeLookupAndSingleflight(t *testing.T) {
	id := channel.ChannelID{ID: "hot", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	block := make(chan struct{})
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 4,
				LeaderEpoch:  5,
				Replicas:     []uint64{2, 3},
				ISR:          []uint64{2, 3},
				Leader:       2,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
			},
		},
		getBlock: block,
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
	}

	const workers = 8
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := syncer.ActivateByKey(context.Background(), key, channelruntime.ActivationSourceFetch)
			errCh <- err
		}()
	}
	close(block)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.Equal(t, 1, source.getCalls)
	require.Len(t, cluster.runtimeUpserts, 1)
}

func TestChannelMetaSyncRefreshClearsAppliedLocalOnHashSlotTableVersionChange(t *testing.T) {
	staleKey := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "stale", Type: 1})
	freshID := channel.ChannelID{ID: "fresh", Type: 1}
	source := &fakeChannelMetaSource{
		version: 2,
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			freshID: {
				ChannelID:    freshID.ID,
				ChannelType:  int64(freshID.Type),
				ChannelEpoch: 3,
				LeaderEpoch:  4,
				Replicas:     []uint64{2},
				ISR:          []uint64{2},
				Leader:       2,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:                   source,
		cluster:                  cluster,
		localNode:                2,
		lastHashSlotTableVersion: 1,
		appliedLocal: map[channel.ChannelKey]struct{}{
			staleKey: {},
		},
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), freshID)
	require.NoError(t, err)
	require.Equal(t, map[channel.ChannelKey]struct{}{got.Key: {}}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncRefreshReturnsExistingAuthoritativeMetaWithoutBootstrap(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  3,
		Replicas:     []uint64{2, 4},
		ISR:          []uint64{2, 4},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: meta},
	}
	bootstrapStore := &fakeBootstrapStore{getErr: metadb.ErrNotFound}
	bootstrapCluster := &fakeBootstrapCluster{slotID: 9, peers: []uint64{2, 4}, leader: 2}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		bootstrap: newChannelMetaBootstrapper(bootstrapCluster, bootstrapStore, 2, time.Now, wklog.NewNop()),
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Epoch)
	require.Equal(t, []channel.Meta{got}, cluster.applied)
	require.Empty(t, bootstrapStore.upserts)
	require.Zero(t, bootstrapCluster.leaderCalls)
}

func TestChannelMetaSyncRefreshDoesNotRewriteLeaderOnSlotLeaderChange(t *testing.T) {
	id := channel.ChannelID{ID: "stable", Type: 1}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  11,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	drifted := authoritative
	drifted.Leader = 2
	drifted.LeaderEpoch = authoritative.LeaderEpoch + 1
	drifted.LeaseUntilMS = time.Now().Add(channelMetaBootstrapLease).UnixMilli()

	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: authoritative},
			{meta: drifted},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:  source,
		cluster: cluster,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 9, peers: []uint64{2, 3}, leader: 2},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, projectChannelMeta(authoritative), got)
	require.Equal(t, []channel.Meta{projectChannelMeta(authoritative)}, cluster.applied)
	require.Empty(t, source.upserts)
}

func TestRefreshAuthoritativeByKeyOnSlotLeaderChangeDoesNotRewriteChannelLeader(t *testing.T) {
	id := channel.ChannelID{ID: "slot-refresh", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	drifted := authoritative
	drifted.Leader = 2
	drifted.LeaderEpoch = authoritative.LeaderEpoch + 1
	drifted.LeaseUntilMS = time.Now().Add(channelMetaBootstrapLease).UnixMilli()

	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: authoritative},
			{meta: drifted},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:  source,
		cluster: cluster,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 11, peers: []uint64{2, 3}, leader: 2},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
		localNode: 2,
	}

	got, err := syncer.refreshAuthoritativeByKey(context.Background(), key)

	require.NoError(t, err)
	require.Equal(t, projectChannelMeta(authoritative), got)
	require.Equal(t, []channel.Meta{projectChannelMeta(authoritative)}, cluster.applied)
	require.Empty(t, source.upserts)
}

func TestChannelMetaSyncRefreshBootstrapsOnAuthoritativeMiss(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  4,
		Replicas:     []uint64{2, 7},
		ISR:          []uint64{2, 7},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{err: metadb.ErrNotFound},
			{err: metadb.ErrNotFound},
			{meta: authoritative},
			{meta: authoritative},
		},
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: authoritative},
	}
	bootstrapCluster := &fakeBootstrapCluster{slotID: 3, peers: []uint64{2, 7}, leader: 2}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		bootstrap: newChannelMetaBootstrapper(bootstrapCluster, source, 2, time.Now, wklog.NewNop()),
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Epoch)
	require.Equal(t, []channel.Meta{got}, cluster.applied)
	require.Len(t, source.upserts, 1)
	require.Equal(t, authoritative, source.lastGetMeta)
}

func TestChannelMetaSyncRefreshAppliesAuthoritativeRereadInsteadOfBootstrapCandidate(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  11,
		Replicas:     []uint64{2, 8, 9},
		ISR:          []uint64{2, 8},
		Leader:       8,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatU64),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{err: metadb.ErrNotFound},
			{err: metadb.ErrNotFound},
			{meta: authoritative},
			{meta: authoritative},
		},
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: authoritative},
	}
	bootstrapCluster := &fakeBootstrapCluster{slotID: 5, peers: []uint64{2, 7, 8}, leader: 2}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		bootstrap: newChannelMetaBootstrapper(bootstrapCluster, source, 2, time.Now, wklog.NewNop()),
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, authoritative.ChannelEpoch, got.Epoch)
	require.Equal(t, authoritative.LeaderEpoch, got.LeaderEpoch)
	require.Equal(t, channel.NodeID(authoritative.Leader), got.Leader)
	require.Equal(t, projectChannelMeta(authoritative), got)
	require.Len(t, source.upserts, 1)
	require.Equal(t, []channel.Meta{projectChannelMeta(authoritative)}, cluster.applied)
}

func TestChannelMetaSyncStopCancelsPendingSlotLeaderRefresh(t *testing.T) {
	id := channel.ChannelID{ID: "stop-refresh", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 4,
				LeaderEpoch:  7,
				Replicas:     []uint64{2, 7},
				ISR:          []uint64{2, 7},
				Leader:       7,
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
				LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
			},
		},
		getBlock:       block,
		getStarted:     started,
		getRespectsCtx: true,
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		localNode:       2,
		refreshInterval: 0,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 3, peers: []uint64{2, 7}, leader: 7},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
	}
	syncer.mu.Lock()
	syncer.trackAppliedLocalKeyLocked(key)
	syncer.mu.Unlock()

	require.NoError(t, syncer.Start())
	syncer.scheduleSlotLeaderRefresh(3)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slot leader refresh did not start")
	}

	require.NoError(t, syncer.Stop())
	close(block)
	time.Sleep(50 * time.Millisecond)

	require.Empty(t, cluster.runtimeUpserts)
}

func TestChannelMetaSyncScheduleSlotLeaderRefreshRerunsWhenDirty(t *testing.T) {
	id := channel.ChannelID{ID: "dirty-slot-refresh", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	first := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 4,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 7},
		ISR:          []uint64{2, 7},
		Leader:       7,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	second := first
	second.Leader = 2
	second.LeaderEpoch++
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: first},
			{meta: second},
		},
		getBlock:       block,
		getStarted:     started,
		getRespectsCtx: true,
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		localNode:       2,
		refreshInterval: 0,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 3, peers: []uint64{2, 7}, leader: 7},
			source,
			2,
			time.Now,
			wklog.NewNop(),
		),
	}
	syncer.mu.Lock()
	syncer.trackAppliedLocalKeyLocked(key)
	syncer.mu.Unlock()

	require.NoError(t, syncer.Start())
	defer func() {
		require.NoError(t, syncer.Stop())
	}()

	syncer.scheduleSlotLeaderRefresh(3)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slot leader refresh did not start")
	}

	syncer.scheduleSlotLeaderRefresh(3)
	close(block)

	require.Eventually(t, func() bool {
		source.mu.Lock()
		getCalls := source.getCalls
		source.mu.Unlock()
		cluster.mu.Lock()
		upserts := len(cluster.runtimeUpserts)
		cluster.mu.Unlock()
		return getCalls >= 2 && upserts >= 2
	}, time.Second, 10*time.Millisecond)
}

func TestChannelMetaSyncRefreshRenewsExpiredLeaderLeaseBeforeApply(t *testing.T) {
	now := time.UnixMilli(1_700_000_555_000).UTC()
	id := channel.ChannelID{ID: "lease-refresh", Type: 1}
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: now.Add(-time.Second).UnixMilli(),
	}
	renewed := expired
	renewed.LeaseUntilMS = now.Add(channelMetaBootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: expired},
			{meta: renewed},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:  source,
		cluster: cluster,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 9, peers: []uint64{2, 3}, leader: 2},
			source,
			2,
			func() time.Time { return now },
			wklog.NewNop(),
		),
		localNode: 2,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Len(t, source.upserts, 1)
	require.Equal(t, renewed.LeaseUntilMS, source.upserts[0].LeaseUntilMS)
	require.Equal(t, expired.Replicas, source.upserts[0].Replicas)
	require.Equal(t, expired.ISR, source.upserts[0].ISR)
	require.Equal(t, expired.Leader, source.upserts[0].Leader)
	require.Equal(t, expired.ChannelEpoch, source.upserts[0].ChannelEpoch)
	require.Equal(t, expired.LeaderEpoch, source.upserts[0].LeaderEpoch)
	require.Equal(t, projectChannelMeta(renewed), got)
	require.Equal(t, []channel.Meta{projectChannelMeta(renewed)}, cluster.applied)
}

func TestChannelMetaSyncRefreshDoesNotApplyPartialMetadataWhenBootstrapFails(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	bootstrapErr := errors.New("write failed")
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{err: metadb.ErrNotFound},
			{err: metadb.ErrNotFound},
		},
		upsertErr: bootstrapErr,
	}
	bootstrapCluster := &fakeBootstrapCluster{slotID: 7, peers: []uint64{2, 4}, leader: 2}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		bootstrap: newChannelMetaBootstrapper(bootstrapCluster, source, 2, time.Now, wklog.NewNop()),
		localNode: 2,
	}

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.ErrorContains(t, err, "upsert runtime metadata")
	require.ErrorIs(t, err, bootstrapErr)
	require.Empty(t, cluster.applied)
	require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
	require.Len(t, source.upserts, 1)
}

func TestChannelMetaSyncRefreshPreservesRetryableBootstrapErrorsWithoutApplyingLocalState(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{name: "no leader", err: raftcluster.ErrNoLeader},
		{name: "slot not found", err: raftcluster.ErrSlotNotFound},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			id := channel.ChannelID{ID: "g1", Type: 2}
			source := &fakeChannelMetaSource{
				getResults: []fakeChannelMetaGetResult{
					{err: metadb.ErrNotFound},
					{err: metadb.ErrNotFound},
				},
			}
			bootstrapCluster := &fakeBootstrapCluster{
				slotID:    8,
				peers:     []uint64{2, 4},
				leaderErr: tt.err,
			}
			cluster := &fakeChannelMetaCluster{}
			syncer := &channelMetaSync{
				source:    source,
				cluster:   cluster,
				bootstrap: newChannelMetaBootstrapper(bootstrapCluster, source, 2, time.Now, wklog.NewNop()),
				localNode: 2,
			}

			_, err := syncer.RefreshChannelMeta(context.Background(), id)
			require.ErrorIs(t, err, tt.err)
			require.Empty(t, cluster.applied)
			require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
			require.Empty(t, source.upserts)
		})
	}
}

func TestChannelMetaSyncSyncOnceClearsAppliedLocalOnHashSlotTableVersionChange(t *testing.T) {
	staleKey := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "stale", Type: 1})
	freshID := channel.ChannelID{ID: "fresh", Type: 1}
	source := &fakeChannelMetaSource{
		version: 2,
		list: []metadb.ChannelRuntimeMeta{
			{
				ChannelID:    freshID.ID,
				ChannelType:  int64(freshID.Type),
				ChannelEpoch: 3,
				LeaderEpoch:  4,
				Replicas:     []uint64{2},
				ISR:          []uint64{2},
				Leader:       2,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:                   source,
		cluster:                  cluster,
		localNode:                2,
		lastHashSlotTableVersion: 1,
		appliedLocal: map[channel.ChannelKey]struct{}{
			staleKey: {},
		},
	}

	require.NoError(t, syncer.syncOnce(context.Background()))
	require.Equal(t, []channel.Meta{projectChannelMeta(source.list[0])}, cluster.applied)
	require.Empty(t, cluster.removed)
	require.Equal(t, map[channel.ChannelKey]struct{}{
		channelhandler.KeyFromChannelID(freshID): {},
	}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestChannelMetaSyncSyncOnceRenewsLocalLeaderLeaseBeforeExpiry(t *testing.T) {
	now := time.UnixMilli(1_700_000_666_000).UTC()
	id := channel.ChannelID{ID: "lease-sync", Type: 1}
	expiring := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 3,
		LeaderEpoch:  4,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: now.Add(200 * time.Millisecond).UnixMilli(),
	}
	renewed := expiring
	renewed.LeaseUntilMS = now.Add(channelMetaBootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		list: []metadb.ChannelRuntimeMeta{expiring},
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: renewed,
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:  source,
		cluster: cluster,
		bootstrap: newChannelMetaBootstrapper(
			&fakeBootstrapCluster{slotID: 10, peers: []uint64{2, 3}, leader: 2},
			source,
			2,
			func() time.Time { return now },
			wklog.NewNop(),
		),
		localNode:       2,
		refreshInterval: time.Second,
	}

	err := syncer.syncOnce(context.Background())

	require.NoError(t, err)
	require.Len(t, source.upserts, 1)
	require.Equal(t, renewed.LeaseUntilMS, source.upserts[0].LeaseUntilMS)
	require.Equal(t, expiring.Replicas, source.upserts[0].Replicas)
	require.Equal(t, expiring.ISR, source.upserts[0].ISR)
	require.Equal(t, expiring.Leader, source.upserts[0].Leader)
	require.Equal(t, expiring.ChannelEpoch, source.upserts[0].ChannelEpoch)
	require.Equal(t, expiring.LeaderEpoch, source.upserts[0].LeaderEpoch)
	require.Equal(t, []channel.Meta{projectChannelMeta(renewed)}, cluster.applied)
}

func TestChannelMetaSyncSyncOnceRemovesChannelsNoLongerAssignedLocally(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "local", Type: 1})
	source := &fakeChannelMetaSource{
		list: []metadb.ChannelRuntimeMeta{
			{
				ChannelID:    "other",
				ChannelType:  1,
				ChannelEpoch: 5,
				LeaderEpoch:  6,
				Replicas:     []uint64{9},
				ISR:          []uint64{9},
				Leader:       9,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
		appliedLocal: map[channel.ChannelKey]struct{}{
			key: {},
		},
	}

	require.NoError(t, syncer.syncOnce(context.Background()))
	require.Equal(t, []channel.ChannelKey{key}, cluster.removed)
	require.Equal(t, map[channel.ChannelKey]struct{}{}, nonNilAppliedLocal(syncer.appliedLocal))
}

func TestChannelMetaSyncStopRemovesAppliedLocalChannels(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "local", Type: 1})
	done := make(chan struct{})
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		cluster: cluster,
		cancel: func() {
			close(done)
		},
		done: done,
		appliedLocal: map[channel.ChannelKey]struct{}{
			key: {},
		},
	}

	require.NoError(t, syncer.Stop())
	require.Equal(t, []channel.ChannelKey{key}, cluster.removed)
	require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestMemoryGenerationStoreConcurrentAccess(t *testing.T) {
	store := newMemoryGenerationStore()
	keys := []channel.ChannelKey{
		"a",
		"b",
		"c",
		"d",
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := keys[(worker+i)%len(keys)]
				require.NoError(t, store.Store(key, uint64(worker+i)))
				_, err := store.Load(key)
				require.NoError(t, err)
			}
		}(worker)
	}
	wg.Wait()
}

func TestChannelMetaSyncConcurrentRefreshAndStop(t *testing.T) {
	id := channel.ChannelID{ID: "race", Type: 1}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatU64),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: meta,
		},
		list: []metadb.ChannelRuntimeMeta{meta},
	}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		localNode:       2,
		refreshInterval: time.Millisecond,
	}

	require.NoError(t, syncer.Start())

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = syncer.RefreshChannelMeta(context.Background(), id)
			}
		}()
	}

	require.NoError(t, syncer.Stop())
	wg.Wait()
}

func TestChannelMetaSyncStartAllowsTransientLeaderlessSource(t *testing.T) {
	source := &fakeChannelMetaSource{listErr: raftcluster.ErrNoLeader}
	cluster := &fakeChannelMetaCluster{}
	syncer := &channelMetaSync{
		source:          source,
		cluster:         cluster,
		localNode:       2,
		refreshInterval: time.Hour,
	}

	require.NoError(t, syncer.Start())
	require.NoError(t, syncer.Stop())
	require.Empty(t, cluster.applied)
	require.Empty(t, cluster.removed)
}

func TestChannelMetaBootstrapperDerivesInitialMetaFromSlotTopology(t *testing.T) {
	now := time.UnixMilli(1_700_000_123_000).UTC()
	store := &fakeBootstrapStore{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:    "u2@u1",
			ChannelType:  1,
			ChannelEpoch: 1,
			LeaderEpoch:  1,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2, 3},
			Leader:       2,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     uint64(channel.MessageSeqFormatLegacyU32),
			LeaseUntilMS: now.Add(channelMetaBootstrapLease).UnixMilli(),
		},
	}
	cluster := &fakeBootstrapCluster{
		slotID: 9,
		peers:  []uint64{1, 2, 3},
		leader: 2,
	}

	bootstrapper := newChannelMetaBootstrapper(cluster, store, 2, func() time.Time { return now }, wklog.NewNop())

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "u2@u1", Type: 1})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, store.authoritativeMeta, meta)
	require.Len(t, store.upserts, 1)
	require.Equal(t, []uint64{1, 2, 3}, store.upserts[0].Replicas)
	require.Equal(t, []uint64{1, 2, 3}, store.upserts[0].ISR)
	require.Equal(t, uint64(2), store.upserts[0].Leader)
	require.Equal(t, int64(2), store.upserts[0].MinISR)
	require.Equal(t, uint64(1), store.upserts[0].ChannelEpoch)
	require.Equal(t, uint64(1), store.upserts[0].LeaderEpoch)
	require.Equal(t, uint8(channel.StatusActive), store.upserts[0].Status)
	require.Equal(t, uint64(channel.MessageSeqFormatLegacyU32), store.upserts[0].Features)
	require.Equal(t, now.Add(channelMetaBootstrapLease).UnixMilli(), store.upserts[0].LeaseUntilMS)
}

func TestChannelMetaBootstrapperClampsMinISRForSingleReplica(t *testing.T) {
	store := &fakeBootstrapStore{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   "solo",
			ChannelType: 1,
			Replicas:    []uint64{7},
			ISR:         []uint64{7},
			Leader:      7,
			MinISR:      1,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	cluster := &fakeBootstrapCluster{
		slotID: 3,
		peers:  []uint64{7},
		leader: 7,
	}

	bootstrapper := newChannelMetaBootstrapper(cluster, store, 2, time.Now, wklog.NewNop())

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "solo", Type: 1})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(1), meta.MinISR)
	require.Len(t, store.upserts, 1)
	require.Equal(t, int64(1), store.upserts[0].MinISR)
}

func TestChannelMetaBootstrapperDefaultsMinISRToTwo(t *testing.T) {
	store := &fakeBootstrapStore{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   "defaulted",
			ChannelType: 1,
			Replicas:    []uint64{4, 5, 6},
			ISR:         []uint64{4, 5, 6},
			Leader:      5,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	cluster := &fakeBootstrapCluster{
		slotID: 7,
		peers:  []uint64{4, 5, 6},
		leader: 5,
	}

	bootstrapper := newChannelMetaBootstrapper(cluster, store, 0, time.Now, wklog.NewNop())

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "defaulted", Type: 1})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(2), meta.MinISR)
	require.Len(t, store.upserts, 1)
	require.Equal(t, int64(2), store.upserts[0].MinISR)
}

func TestChannelMetaBootstrapperFailsWhenSlotHasNoPeers(t *testing.T) {
	store := &fakeBootstrapStore{getErr: metadb.ErrNotFound}
	cluster := &fakeBootstrapCluster{slotID: 5}
	bootstrapper := newChannelMetaBootstrapper(cluster, store, 2, time.Now, wklog.NewNop())

	_, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "empty", Type: 1})
	require.Error(t, err)
	require.ErrorContains(t, err, "slot peers")
	require.False(t, created)
	require.Empty(t, store.upserts)
}

func TestChannelMetaBootstrapperPropagatesRetryableClusterErrors(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{name: "no leader", err: raftcluster.ErrNoLeader},
		{name: "slot not found", err: raftcluster.ErrSlotNotFound},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeBootstrapStore{getErr: metadb.ErrNotFound}
			cluster := &fakeBootstrapCluster{
				slotID:    11,
				peers:     []uint64{1, 2, 3},
				leaderErr: tt.err,
			}
			bootstrapper := newChannelMetaBootstrapper(cluster, store, 2, time.Now, wklog.NewNop())

			_, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "unstable", Type: 1})
			require.ErrorIs(t, err, tt.err)
			require.False(t, created)
			require.Empty(t, store.upserts)
		})
	}
}

func TestChannelMetaBootstrapperLogsMissingBootstrappedAndFailedEvents(t *testing.T) {
	logger := newBootstrapRecordingLogger("app")
	id := channel.ChannelID{ID: "u2@u1", Type: 1}

	successStore := &fakeBootstrapStore{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Replicas:    []uint64{1, 2, 3},
			ISR:         []uint64{1, 2, 3},
			Leader:      2,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	successCluster := &fakeBootstrapCluster{
		slotID: 21,
		peers:  []uint64{1, 2, 3},
		leader: 2,
	}
	successBootstrapper := newChannelMetaBootstrapper(successCluster, successStore, 2, time.Now, logger)

	_, created, err := successBootstrapper.EnsureChannelRuntimeMeta(context.Background(), id)
	require.NoError(t, err)
	require.True(t, created)

	failureStore := &fakeBootstrapStore{getErr: metadb.ErrNotFound}
	failureCluster := &fakeBootstrapCluster{
		slotID: 22,
		peers:  []uint64{9, 10},
		leader: 9,
	}
	failureBootstrapper := newChannelMetaBootstrapper(failureCluster, failureStore, 2, time.Now, logger)
	failureStore.upsertErr = errors.New("write failed")

	_, created, err = failureBootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "failed", Type: 2})
	require.Error(t, err)
	require.False(t, created)

	entries := logger.entries()
	require.Len(t, entries, 4)

	require.Equal(t, "INFO", entries[0].level)
	require.Equal(t, "missing runtime metadata; bootstrapping", entries[0].msg)
	requireBootstrapFieldEquals(t, entries[0], "event", "app.channelmeta.bootstrap.missing")
	requireBootstrapFieldEquals(t, entries[0], "channelID", "u2@u1")
	requireBootstrapFieldEquals(t, entries[0], "channelType", int64(1))
	requireBootstrapFieldEquals(t, entries[0], "slotID", uint64(21))
	requireBootstrapFieldEquals(t, entries[0], "leader", uint64(0))
	requireBootstrapFieldEquals(t, entries[0], "replicaCount", 3)
	requireBootstrapFieldEquals(t, entries[0], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[0], "created", false)

	require.Equal(t, "INFO", entries[1].level)
	require.Equal(t, "bootstrapped runtime metadata", entries[1].msg)
	requireBootstrapFieldEquals(t, entries[1], "event", "app.channelmeta.bootstrap.bootstrapped")
	requireBootstrapFieldEquals(t, entries[1], "channelID", "u2@u1")
	requireBootstrapFieldEquals(t, entries[1], "channelType", int64(1))
	requireBootstrapFieldEquals(t, entries[1], "slotID", uint64(21))
	requireBootstrapFieldEquals(t, entries[1], "leader", uint64(2))
	requireBootstrapFieldEquals(t, entries[1], "replicaCount", 3)
	requireBootstrapFieldEquals(t, entries[1], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[1], "created", true)

	require.Equal(t, "INFO", entries[2].level)
	require.Equal(t, "missing runtime metadata; bootstrapping", entries[2].msg)
	requireBootstrapFieldEquals(t, entries[2], "event", "app.channelmeta.bootstrap.missing")
	requireBootstrapFieldEquals(t, entries[2], "channelID", "failed")
	requireBootstrapFieldEquals(t, entries[2], "channelType", int64(2))
	requireBootstrapFieldEquals(t, entries[2], "slotID", uint64(22))
	requireBootstrapFieldEquals(t, entries[2], "leader", uint64(0))
	requireBootstrapFieldEquals(t, entries[2], "replicaCount", 2)
	requireBootstrapFieldEquals(t, entries[2], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[2], "created", false)

	require.Equal(t, "ERROR", entries[3].level)
	require.Equal(t, "failed to bootstrap runtime metadata", entries[3].msg)
	requireBootstrapFieldEquals(t, entries[3], "event", "app.channelmeta.bootstrap.failed")
	requireBootstrapFieldEquals(t, entries[3], "channelID", "failed")
	requireBootstrapFieldEquals(t, entries[3], "channelType", int64(2))
	requireBootstrapFieldEquals(t, entries[3], "slotID", uint64(22))
	requireBootstrapFieldEquals(t, entries[3], "leader", uint64(9))
	requireBootstrapFieldEquals(t, entries[3], "replicaCount", 2)
	requireBootstrapFieldEquals(t, entries[3], "minISR", int64(2))
}

type fakeChannelMetaSource struct {
	mu             sync.Mutex
	get            map[channel.ChannelID]metadb.ChannelRuntimeMeta
	list           []metadb.ChannelRuntimeMeta
	getErr         error
	listErr        error
	version        uint64
	getResults     []fakeChannelMetaGetResult
	getCalls       int
	listCalls      int
	getBlock       <-chan struct{}
	getStarted     chan<- struct{}
	getRespectsCtx bool
	lastGetMeta    metadb.ChannelRuntimeMeta
	upsertErr      error
	upserts        []metadb.ChannelRuntimeMeta
}

type fakeChannelMetaGetResult struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f *fakeChannelMetaSource) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if f.getStarted != nil {
		select {
		case f.getStarted <- struct{}{}:
		default:
		}
	}
	if f.getBlock != nil {
		if f.getRespectsCtx {
			select {
			case <-f.getBlock:
			case <-ctx.Done():
				return metadb.ChannelRuntimeMeta{}, ctx.Err()
			}
		} else {
			<-f.getBlock
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getCalls <= len(f.getResults) {
		result := f.getResults[f.getCalls-1]
		if result.err != nil {
			return metadb.ChannelRuntimeMeta{}, result.err
		}
		f.lastGetMeta = result.meta
		return result.meta, nil
	}
	if f.getErr != nil {
		return metadb.ChannelRuntimeMeta{}, f.getErr
	}
	meta := f.get[channel.ChannelID{ID: channelID, Type: uint8(channelType)}]
	f.lastGetMeta = meta
	return meta, nil
}

func (f *fakeChannelMetaSource) ListChannelRuntimeMeta(context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]metadb.ChannelRuntimeMeta(nil), f.list...), nil
}

func (f *fakeChannelMetaSource) HashSlotTableVersion() uint64 {
	return f.version
}

func (f *fakeChannelMetaSource) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

func (f *fakeChannelMetaSource) UpsertChannelRuntimeMetaIfLocalLeader(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

type fakeChannelMetaCluster struct {
	mu             sync.Mutex
	applied        []channel.Meta
	removed        []channel.ChannelKey
	routingApplied []channel.Meta
	runtimeUpserts []channel.Meta
	runtimeRemoved []channel.ChannelKey
	applyErr       error
	removeErr      error
}

type fakeChannelHandlerRuntime struct {
	handles map[channel.ChannelKey]channel.HandlerChannel
}

func (f fakeChannelHandlerRuntime) Channel(key channel.ChannelKey) (channel.HandlerChannel, bool) {
	handle, ok := f.handles[key]
	return handle, ok
}

type fakeHandlerChannel struct {
	key   channel.ChannelKey
	meta  channel.Meta
	state channel.ReplicaState
}

func (f fakeHandlerChannel) ID() channel.ChannelKey { return f.key }

func (f fakeHandlerChannel) Meta() channel.Meta { return f.meta }

func (f fakeHandlerChannel) Status() channel.ReplicaState { return f.state }

func (f fakeHandlerChannel) Append(context.Context, []channel.Record) (channel.CommitResult, error) {
	return channel.CommitResult{}, channel.ErrInvalidConfig
}

func (f *fakeChannelMetaCluster) ApplyMeta(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) RemoveLocal(key channel.ChannelKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	return f.removeErr
}

func (f *fakeChannelMetaCluster) ApplyRoutingMeta(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, meta)
	f.routingApplied = append(f.routingApplied, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) EnsureLocalRuntime(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runtimeUpserts = append(f.runtimeUpserts, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) RemoveLocalRuntime(key channel.ChannelKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	f.runtimeRemoved = append(f.runtimeRemoved, key)
	return f.removeErr
}

func nonNilAppliedLocal(values map[channel.ChannelKey]struct{}) map[channel.ChannelKey]struct{} {
	if len(values) == 0 {
		return map[channel.ChannelKey]struct{}{}
	}
	return cloneAppliedLocalSet(values)
}

type fakeBootstrapStore struct {
	getErr            error
	upsertErr         error
	authoritativeMeta metadb.ChannelRuntimeMeta
	upserts           []metadb.ChannelRuntimeMeta
}

func (f *fakeBootstrapStore) GetChannelRuntimeMeta(_ context.Context, _ string, _ int64) (metadb.ChannelRuntimeMeta, error) {
	if f.getErr != nil && len(f.upserts) == 0 {
		return metadb.ChannelRuntimeMeta{}, f.getErr
	}
	return f.authoritativeMeta, nil
}

func (f *fakeBootstrapStore) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

type fakeBootstrapCluster struct {
	slotID      multiraft.SlotID
	peers       []uint64
	leader      uint64
	leaderErr   error
	leaderCalls int
	nodes       []controllermeta.ClusterNode
	nodesErr    error
	nodesCalls  int
}

func (f *fakeBootstrapCluster) SlotForKey(string) multiraft.SlotID {
	return f.slotID
}

func (f *fakeBootstrapCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	out := make([]multiraft.NodeID, 0, len(f.peers))
	for _, peer := range f.peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}

func (f *fakeBootstrapCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	f.leaderCalls++
	if f.leaderErr != nil {
		return 0, f.leaderErr
	}
	return multiraft.NodeID(f.leader), nil
}

func (f *fakeBootstrapCluster) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	f.nodesCalls++
	if f.nodesErr != nil {
		return nil, f.nodesErr
	}
	return append([]controllermeta.ClusterNode(nil), f.nodes...), nil
}

type bootstrapRecordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e bootstrapRecordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type bootstrapRecordingLoggerSink struct {
	mu      sync.Mutex
	entries []bootstrapRecordedLogEntry
}

type bootstrapRecordingLogger struct {
	module string
	base   []wklog.Field
	sink   *bootstrapRecordingLoggerSink
}

func newBootstrapRecordingLogger(module string) *bootstrapRecordingLogger {
	return &bootstrapRecordingLogger{module: module, sink: &bootstrapRecordingLoggerSink{}}
}

func (r *bootstrapRecordingLogger) Debug(msg string, fields ...wklog.Field) {
	r.log("DEBUG", msg, fields...)
}
func (r *bootstrapRecordingLogger) Info(msg string, fields ...wklog.Field) {
	r.log("INFO", msg, fields...)
}
func (r *bootstrapRecordingLogger) Warn(msg string, fields ...wklog.Field) {
	r.log("WARN", msg, fields...)
}
func (r *bootstrapRecordingLogger) Error(msg string, fields ...wklog.Field) {
	r.log("ERROR", msg, fields...)
}
func (r *bootstrapRecordingLogger) Fatal(msg string, fields ...wklog.Field) {
	r.log("FATAL", msg, fields...)
}

func (r *bootstrapRecordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return r
	}
	module := name
	if r.module != "" {
		module = r.module + "." + name
	}
	return &bootstrapRecordingLogger{module: module, base: append([]wklog.Field(nil), r.base...), sink: r.sink}
}

func (r *bootstrapRecordingLogger) With(fields ...wklog.Field) wklog.Logger {
	return &bootstrapRecordingLogger{
		module: r.module,
		base:   append(append([]wklog.Field(nil), r.base...), fields...),
		sink:   r.sink,
	}
}

func (r *bootstrapRecordingLogger) Sync() error { return nil }

func (r *bootstrapRecordingLogger) log(level, msg string, fields ...wklog.Field) {
	if r == nil || r.sink == nil {
		return
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	r.sink.entries = append(r.sink.entries, bootstrapRecordedLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), r.base...), fields...),
	})
}

func (r *bootstrapRecordingLogger) entries() []bootstrapRecordedLogEntry {
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := make([]bootstrapRecordedLogEntry, len(r.sink.entries))
	copy(out, r.sink.entries)
	return out
}

func requireBootstrapFieldEquals(t *testing.T, entry bootstrapRecordedLogEntry, key string, want any) {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "missing log field %s", key)
	require.Equal(t, want, field.Value)
}
