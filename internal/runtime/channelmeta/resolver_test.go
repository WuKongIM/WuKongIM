package channelmeta

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

func TestResolverRefreshProjectsLeaderEpochLeaseAndApply(t *testing.T) {
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
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{{ID: "u1", Type: 1}: meta},
	}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		LocalNode: 2,
	})

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
	require.Equal(t, []channel.Meta{got}, runtime.applied)
	require.Equal(t, map[channel.ChannelKey]struct{}{got.Key: {}}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestResolverRefreshInvokesInjectedRepairer(t *testing.T) {
	id := channel.ChannelID{ID: "repair-refresh", Type: 1}
	authoritative := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3, 4},
		ISR:          []uint64{2, 3, 4},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	repaired := authoritative
	repaired.Leader = 4
	repaired.LeaderEpoch = 8
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: authoritative},
	}
	repairer := &resolverRepairerFake{meta: repaired, changed: true}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   &resolverRuntimeFake{},
		Repairer:  repairer,
		LocalNode: 2,
		RepairPolicy: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 3, "leader_dead"
		},
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, channel.NodeID(4), got.Leader)
	require.Len(t, repairer.snapshotCalls(), 1)
	require.Equal(t, "leader_dead", repairer.snapshotCalls()[0].reason)
}

func TestResolverSkipsAfterLocalApplyWhenRepairUnavailableOrPolicyDeclines(t *testing.T) {
	id := channel.ChannelID{ID: "repair-gated", Type: 1}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     []uint64{2},
		ISR:          []uint64{2},
		Leader:       2,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
	}
	testCases := []struct {
		name     string
		repairer Repairer
		policy   RepairPolicy
	}{
		{name: "repairer missing", policy: func(metadb.ChannelRuntimeMeta) (bool, string) { return true, "leader_dead" }},
		{name: "policy missing", repairer: &resolverRepairerFake{meta: meta}},
		{
			name:     "policy declines",
			repairer: &resolverRepairerFake{meta: meta},
			policy:   func(metadb.ChannelRuntimeMeta) (bool, string) { return false, "" },
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			var afterLocalApplyCalls int
			syncer := NewSync(SyncOptions{
				Source:       &resolverSourceFake{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: meta}},
				Runtime:      &resolverRuntimeFake{},
				Repairer:     tt.repairer,
				LocalNode:    2,
				RepairPolicy: tt.policy,
				AfterLocalApply: func(channel.Meta) {
					afterLocalApplyCalls++
				},
			})

			_, err := syncer.RefreshChannelMeta(context.Background(), id)

			require.NoError(t, err)
			require.Zero(t, afterLocalApplyCalls)
		})
	}
}

func TestResolverRefreshRepairsObservedRuntimeLeaderDrift(t *testing.T) {
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
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: authoritative},
	}
	runtime := &resolverRuntimeFake{
		channels: map[channel.ChannelKey]ChannelObserver{
			key: channelObserverFake{
				meta: ProjectChannelMeta(authoritative),
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
	}
	repairer := &resolverRepairerFake{meta: repaired, changed: true}
	var syncer *Sync
	syncer = NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		Repairer:  repairer,
		LocalNode: 2,
		RepairPolicy: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			reason := syncer.LocalRuntimeLeaderRepairReason(meta)
			return reason != "", reason
		},
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Len(t, repairer.snapshotCalls(), 1)
	require.Equal(t, "leader_drift", repairer.snapshotCalls()[0].reason)
	require.Equal(t, channel.NodeID(2), got.Leader)
	require.Equal(t, []channel.Meta{got}, runtime.applied)
}

func TestResolverRefreshPropagatesRepairError(t *testing.T) {
	id := channel.ChannelID{ID: "repair-none", Type: 1}
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 5,
				LeaderEpoch:  4,
				Replicas:     []uint64{2, 3},
				ISR:          []uint64{2, 3},
				Leader:       3,
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   &resolverRuntimeFake{},
		Repairer:  &resolverRepairerFake{err: channel.ErrNoSafeChannelLeader},
		LocalNode: 2,
		RepairPolicy: func(metadb.ChannelRuntimeMeta) (bool, string) {
			return true, "leader_dead"
		},
	})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
}

func TestResolverRefreshSingleflightsConcurrentRepair(t *testing.T) {
	id := channel.ChannelID{ID: "repair-sf", Type: 1}
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 6,
				LeaderEpoch:  3,
				Replicas:     []uint64{2, 3, 4},
				ISR:          []uint64{2, 3, 4},
				Leader:       3,
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	block := make(chan struct{})
	repairer := &resolverRepairerFake{
		block: block,
		meta: metadb.ChannelRuntimeMeta{
			ChannelID:    id.ID,
			ChannelType:  int64(id.Type),
			ChannelEpoch: 6,
			LeaderEpoch:  4,
			Replicas:     []uint64{2, 3, 4},
			ISR:          []uint64{2, 3, 4},
			Leader:       4,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
		},
		changed: true,
	}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   &resolverRuntimeFake{},
		Repairer:  repairer,
		LocalNode: 2,
		RepairPolicy: func(metadb.ChannelRuntimeMeta) (bool, string) {
			return true, "leader_dead"
		},
	})

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = syncer.RefreshChannelMeta(context.Background(), id)
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(block)
	wg.Wait()

	require.Len(t, repairer.snapshotCalls(), 1)
}

func TestResolverRefreshCachesRemoteRoutingMetaWithoutRuntime(t *testing.T) {
	id := channel.ChannelID{ID: "remote", Type: 1}
	source := &resolverSourceFake{
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
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		LocalNode: 2,
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, []channel.Meta{got}, runtime.routingApplied)
	require.Empty(t, runtime.runtimeUpserts)
	require.Empty(t, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestRefreshChannelMetaUsesHealthyBusinessCache(t *testing.T) {
	id := channel.ChannelID{ID: "g-fast", Type: 2}
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	source := &resolverSourceFake{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channel.StatusActive), LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}}}
	syncer := NewSync(SyncOptions{Source: source, Runtime: &resolverRuntimeFake{}, LocalNode: 1, Now: func() time.Time { return now }})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, 1, source.getCalls)
}

func TestRefreshChannelMetaReportsCacheHitAndRefreshOutcomes(t *testing.T) {
	id := channel.ChannelID{ID: "g-observe", Type: 2}
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	observer := &recordingMetaRefreshObserver{}
	source := &resolverSourceFake{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channel.StatusActive), LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}}}
	syncer := NewSync(SyncOptions{
		Source:              source,
		Runtime:             &resolverRuntimeFake{},
		LocalNode:           1,
		Now:                 func() time.Time { return now },
		MetaRefreshObserver: observer,
	})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)

	require.Equal(t, []MetaRefreshResult{MetaRefreshAuthoritativeRead, MetaRefreshCacheHit}, observer.results())
}

func TestResolverStartDoesNotScanAuthoritativeMetas(t *testing.T) {
	source := &resolverSourceFake{}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:          source,
		Runtime:         runtime,
		LocalNode:       2,
		RefreshInterval: time.Millisecond,
	})

	require.NoError(t, syncer.Start())
	require.NoError(t, syncer.Stop())
	require.Equal(t, 0, source.listCalls)
	require.Empty(t, runtime.applied)
}

func TestResolverActivateByKeyUsesAuthoritativeLookupAndSingleflight(t *testing.T) {
	id := channel.ChannelID{ID: "hot", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	block := make(chan struct{})
	source := &resolverSourceFake{
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
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		LocalNode: 2,
	})

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
	require.Len(t, runtime.runtimeUpserts, 1)
}

func TestResolverRefreshDoesNotRewriteLeaderOnSlotLeaderChange(t *testing.T) {
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
	drifted.LeaseUntilMS = time.Now().Add(BootstrapLease).UnixMilli()
	source := &resolverSourceFake{
		getResults: []resolverGetResult{
			{meta: authoritative},
			{meta: drifted},
		},
	}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:  source,
		Runtime: runtime,
		Bootstrapper: NewBootstrapper(BootstrapOptions{
			Cluster:       &resolverBootstrapClusterFake{slotID: 9, peers: []uint64{2, 3}, leader: 2},
			Store:         source,
			DefaultMinISR: 2,
			Logger:        wklog.NewNop(),
		}),
		LocalNode: 2,
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, ProjectChannelMeta(authoritative), got)
	require.Equal(t, []channel.Meta{ProjectChannelMeta(authoritative)}, runtime.applied)
	require.Empty(t, source.upserts)
}

func TestResolverRefreshRenewsExpiredLeaderLeaseBeforeApply(t *testing.T) {
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
	renewed.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &resolverSourceFake{
		getResults: []resolverGetResult{
			{meta: expired},
			{meta: renewed},
		},
	}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:  source,
		Runtime: runtime,
		Bootstrapper: NewBootstrapper(BootstrapOptions{
			Cluster:       &resolverBootstrapClusterFake{slotID: 9, peers: []uint64{2, 3}, leader: 2},
			Store:         source,
			DefaultMinISR: 2,
			Now:           func() time.Time { return now },
			Logger:        wklog.NewNop(),
		}),
		LocalNode: 2,
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Len(t, source.upserts, 1)
	require.Equal(t, renewed.LeaseUntilMS, source.upserts[0].LeaseUntilMS)
	require.Equal(t, expired.Replicas, source.upserts[0].Replicas)
	require.Equal(t, expired.ISR, source.upserts[0].ISR)
	require.Equal(t, expired.Leader, source.upserts[0].Leader)
	require.Equal(t, expired.ChannelEpoch, source.upserts[0].ChannelEpoch)
	require.Equal(t, expired.LeaderEpoch, source.upserts[0].LeaderEpoch)
	require.Equal(t, ProjectChannelMeta(renewed), got)
	require.Equal(t, []channel.Meta{ProjectChannelMeta(renewed)}, runtime.applied)
}

func TestResolverRefreshRepairsExpiredLeaderLeaseBeforeBlindRenewal(t *testing.T) {
	now := time.UnixMilli(1_700_000_777_000).UTC()
	id := channel.ChannelID{ID: "lease-repair-first", Type: 1}
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
	repaired := expired
	repaired.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &resolverSourceFake{
		getResults: []resolverGetResult{{meta: expired}},
	}
	runtime := &resolverRuntimeFake{}
	repairer := &resolverRepairerFake{meta: repaired, changed: true}
	syncer := NewSync(SyncOptions{
		Source:  source,
		Runtime: runtime,
		Bootstrapper: NewBootstrapper(BootstrapOptions{
			Cluster:       &resolverBootstrapClusterFake{slotID: 9, peers: []uint64{2, 3}, leader: 2},
			Store:         source,
			DefaultMinISR: 2,
			Now:           func() time.Time { return now },
			Logger:        wklog.NewNop(),
		}),
		Repairer: repairer,
		RepairPolicy: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return MetaLeaseNeedsRenewal(meta.LeaseUntilMS, now, 0), channel.LeaderRepairReasonLeaderLeaseExpired.String()
		},
		LocalNode: 2,
	})

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	calls := repairer.snapshotCalls()
	require.Len(t, calls, 1)
	require.Equal(t, channel.LeaderRepairReasonLeaderLeaseExpired.String(), calls[0].reason)
	require.Empty(t, source.upserts, "expired leader lease must not be renewed before repair evaluation")
	require.Equal(t, ProjectChannelMeta(repaired), got)
	require.Equal(t, []channel.Meta{ProjectChannelMeta(repaired)}, runtime.applied)
}

func TestResolverRefreshDoesNotApplyPartialMetadataWhenBootstrapFails(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	bootstrapErr := errors.New("write failed")
	source := &resolverSourceFake{
		getResults: []resolverGetResult{
			{err: metadb.ErrNotFound},
			{err: metadb.ErrNotFound},
		},
		upsertErr: bootstrapErr,
	}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:  source,
		Runtime: runtime,
		Bootstrapper: NewBootstrapper(BootstrapOptions{
			Cluster:       &resolverBootstrapClusterFake{slotID: 7, peers: []uint64{2, 4}, leader: 2},
			Store:         source,
			DefaultMinISR: 2,
			Logger:        wklog.NewNop(),
		}),
		LocalNode: 2,
	})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.ErrorContains(t, err, "upsert runtime metadata")
	require.ErrorIs(t, err, bootstrapErr)
	require.Empty(t, runtime.applied)
	require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
	require.Len(t, source.upserts, 1)
}

func TestResolverSyncOnceClearsAppliedLocalOnHashSlotTableVersionChange(t *testing.T) {
	staleKey := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "stale", Type: 1})
	freshID := channel.ChannelID{ID: "fresh", Type: 1}
	source := &resolverSourceFake{
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
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		LocalNode: 2,
	})
	syncer.lastHashSlotTableVersion = 1
	syncer.appliedLocal = map[channel.ChannelKey]struct{}{staleKey: {}}

	require.NoError(t, syncer.syncOnce(context.Background()))
	require.Equal(t, []channel.Meta{ProjectChannelMeta(source.list[0])}, runtime.applied)
	require.Empty(t, runtime.removed)
	require.Equal(t, map[channel.ChannelKey]struct{}{
		channelhandler.KeyFromChannelID(freshID): {},
	}, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestResolverSyncOnceRemovesChannelsNoLongerAssignedLocally(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "local", Type: 1})
	source := &resolverSourceFake{
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
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:    source,
		Runtime:   runtime,
		LocalNode: 2,
	})
	syncer.appliedLocal = map[channel.ChannelKey]struct{}{key: {}}

	require.NoError(t, syncer.syncOnce(context.Background()))
	require.Equal(t, []channel.ChannelKey{key}, runtime.removed)
	require.Equal(t, map[channel.ChannelKey]struct{}{}, nonNilAppliedLocal(syncer.appliedLocal))
}

func TestResolverStopRemovesAppliedLocalChannels(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "local", Type: 1})
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{Runtime: runtime})
	syncer.appliedLocal = map[channel.ChannelKey]struct{}{key: {}}

	require.NoError(t, syncer.Stop())

	require.Equal(t, []channel.ChannelKey{key}, runtime.removed)
	require.Nil(t, cloneAppliedLocalSet(syncer.appliedLocal))
}

func TestResolverStartAllowsTransientLeaderlessSource(t *testing.T) {
	source := &resolverSourceFake{listErr: raftcluster.ErrNoLeader}
	runtime := &resolverRuntimeFake{}
	syncer := NewSync(SyncOptions{
		Source:          source,
		Runtime:         runtime,
		LocalNode:       2,
		RefreshInterval: time.Hour,
	})

	require.NoError(t, syncer.Start())
	require.NoError(t, syncer.Stop())
	require.Empty(t, runtime.applied)
	require.Empty(t, runtime.removed)
}

func TestResolverStopWaitsForConcurrentSlotRefreshScheduling(t *testing.T) {
	id := channel.ChannelID{ID: "stop-race", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	source := &resolverSourceFake{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:   id.ID,
				ChannelType: int64(id.Type),
				Replicas:    []uint64{2},
				ISR:         []uint64{2},
				Leader:      2,
				Status:      uint8(channel.StatusActive),
			},
		},
	}
	cluster := &blockingSlotClusterFake{
		slotID:  3,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	syncer := NewSync(SyncOptions{
		Source:          source,
		Runtime:         &resolverRuntimeFake{},
		Cluster:         cluster,
		LocalNode:       2,
		RefreshInterval: time.Hour,
	})
	require.NoError(t, syncer.Start())
	syncer.appliedLocal = map[channel.ChannelKey]struct{}{key: {}}

	scheduleDone := make(chan struct{})
	go func() {
		syncer.ScheduleSlotLeaderRefresh(3)
		close(scheduleDone)
	}()
	<-cluster.entered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- syncer.StopWithoutCleanup()
	}()
	select {
	case err := <-stopDone:
		require.NoError(t, err)
		require.Fail(t, "StopWithoutCleanup returned before in-flight scheduling finished")
	case <-time.After(20 * time.Millisecond):
	}

	close(cluster.release)
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "StopWithoutCleanup did not return after scheduling was released")
	}
	select {
	case <-scheduleDone:
	case <-time.After(time.Second):
		require.Fail(t, "slot refresh scheduling did not finish")
	}
}

type resolverSourceFake struct {
	mu          sync.Mutex
	get         map[channel.ChannelID]metadb.ChannelRuntimeMeta
	list        []metadb.ChannelRuntimeMeta
	getErr      error
	listErr     error
	version     uint64
	getResults  []resolverGetResult
	getCalls    int
	listCalls   int
	getBlock    <-chan struct{}
	lastGetMeta metadb.ChannelRuntimeMeta
	upsertErr   error
	upserts     []metadb.ChannelRuntimeMeta
}

type resolverGetResult struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

type resolverRepairerFake struct {
	mu      sync.Mutex
	calls   []resolverRepairCall
	meta    metadb.ChannelRuntimeMeta
	changed bool
	err     error
	block   <-chan struct{}
}

type resolverRepairCall struct {
	meta   metadb.ChannelRuntimeMeta
	reason string
}

type recordingMetaRefreshObserver struct {
	mu     sync.Mutex
	events []MetaRefreshEvent
}

func (o *recordingMetaRefreshObserver) OnMetaRefresh(event MetaRefreshEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingMetaRefreshObserver) results() []MetaRefreshResult {
	o.mu.Lock()
	defer o.mu.Unlock()
	results := make([]MetaRefreshResult, 0, len(o.events))
	for _, event := range o.events {
		results = append(results, event.Result)
	}
	return results
}

func (s *resolverRepairerFake) RepairIfNeeded(_ context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error) {
	s.mu.Lock()
	s.calls = append(s.calls, resolverRepairCall{meta: meta, reason: reason})
	s.mu.Unlock()
	if s.block != nil {
		<-s.block
	}
	return s.meta, s.changed, s.err
}

func (s *resolverRepairerFake) snapshotCalls() []resolverRepairCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]resolverRepairCall(nil), s.calls...)
}

func (f *resolverSourceFake) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if f.getBlock != nil {
		select {
		case <-f.getBlock:
		case <-ctx.Done():
			return metadb.ChannelRuntimeMeta{}, ctx.Err()
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

func (f *resolverSourceFake) ListChannelRuntimeMeta(context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]metadb.ChannelRuntimeMeta(nil), f.list...), nil
}

func (f *resolverSourceFake) HashSlotTableVersion() uint64 {
	return f.version
}

func (f *resolverSourceFake) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

type resolverRuntimeFake struct {
	mu             sync.Mutex
	applied        []channel.Meta
	removed        []channel.ChannelKey
	routingApplied []channel.Meta
	runtimeUpserts []channel.Meta
	runtimeRemoved []channel.ChannelKey
	channels       map[channel.ChannelKey]ChannelObserver
	removeErr      error
	applyErr       error
}

func (f *resolverRuntimeFake) ApplyRoutingMeta(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, meta)
	f.routingApplied = append(f.routingApplied, meta)
	return f.applyErr
}

func (f *resolverRuntimeFake) EnsureLocalRuntime(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runtimeUpserts = append(f.runtimeUpserts, meta)
	return f.applyErr
}

func (f *resolverRuntimeFake) RemoveLocalRuntime(key channel.ChannelKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	f.runtimeRemoved = append(f.runtimeRemoved, key)
	return f.removeErr
}

func (f *resolverRuntimeFake) Channel(key channel.ChannelKey) (ChannelObserver, bool) {
	if f == nil {
		return nil, false
	}
	ch, ok := f.channels[key]
	return ch, ok
}

type resolverBootstrapClusterFake struct {
	slotID      multiraft.SlotID
	peers       []uint64
	leader      uint64
	leaderErr   error
	leaderCalls int
	nodes       []controllermeta.ClusterNode
	nodesErr    error
	nodesCalls  int
}

func (f *resolverBootstrapClusterFake) SlotForKey(string) multiraft.SlotID {
	return f.slotID
}

func (f *resolverBootstrapClusterFake) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	out := make([]multiraft.NodeID, 0, len(f.peers))
	for _, peer := range f.peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}

func (f *resolverBootstrapClusterFake) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	f.leaderCalls++
	if f.leaderErr != nil {
		return 0, f.leaderErr
	}
	return multiraft.NodeID(f.leader), nil
}

func (f *resolverBootstrapClusterFake) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	f.nodesCalls++
	if f.nodesErr != nil {
		return nil, f.nodesErr
	}
	return append([]controllermeta.ClusterNode(nil), f.nodes...), nil
}

type blockingSlotClusterFake struct {
	slotID  multiraft.SlotID
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingSlotClusterFake) SlotForKey(string) multiraft.SlotID {
	f.once.Do(func() {
		close(f.entered)
		<-f.release
	})
	return f.slotID
}

func (f *blockingSlotClusterFake) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func (f *blockingSlotClusterFake) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}

func nonNilAppliedLocal(values map[channel.ChannelKey]struct{}) map[channel.ChannelKey]struct{} {
	if len(values) == 0 {
		return map[channel.ChannelKey]struct{}{}
	}
	return cloneAppliedLocalSet(values)
}
