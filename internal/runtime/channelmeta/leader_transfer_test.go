package channelmeta

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderTransferRoutesToRemoteSlotLeader(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-remote", Type: 2}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       1,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	remote := &stubChannelLeaderRepairRemote{
		transferResult: LeaderTransferResult{Meta: withTransferLeader(meta, 2), Changed: true},
	}
	repairer := NewLeaderRepairer(LeaderRepairerOptions{
		Cluster: fakeChannelLeaderRepairCluster{slotID: 7, leaderID: 3},
		Remote:  remote,
	})

	got, changed, err := repairer.TransferIfSafe(context.Background(), meta, 2)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, uint64(2), got.Leader)
	require.Equal(t, []LeaderTransferRequest{{
		ChannelID:            id,
		ObservedChannelEpoch: meta.ChannelEpoch,
		ObservedLeaderEpoch:  meta.LeaderEpoch,
		TargetNodeID:         2,
	}}, remote.transferCalls)
}

func TestChannelLeaderTransferReturnsUnchangedWhenTargetAlreadyLeader(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-already-leader", Type: 2}
	latest := activeTransferMeta(id)
	latest.Leader = 2
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	remote := &stubChannelLeaderRepairRemote{}
	repairer := &LeaderRepairer{store: store, remote: remote, localNode: 9, now: time.Now}

	got, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         2,
	})

	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, latest, got.Meta)
	require.Empty(t, store.upserts)
	require.Empty(t, remote.evaluateCalls)
}

func TestChannelLeaderTransferRejectsTargetOutsideReplicas(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-not-replica", Type: 2}
	latest := activeTransferMeta(id)
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	repairer := &LeaderRepairer{store: store, now: time.Now}

	_, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         5,
	})

	require.ErrorIs(t, err, ErrLeaderTransferTargetNotReplica)
	require.Empty(t, store.upserts)
}

func TestChannelLeaderTransferRejectsTargetOutsideISR(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-not-isr", Type: 2}
	latest := activeTransferMeta(id)
	latest.ISR = []uint64{1, 2}
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	repairer := &LeaderRepairer{store: store, now: time.Now}

	_, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         3,
	})

	require.ErrorIs(t, err, ErrLeaderTransferTargetNotISR)
	require.Empty(t, store.upserts)
}

func TestChannelLeaderTransferRejectsInactiveChannel(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-inactive", Type: 2}
	latest := activeTransferMeta(id)
	latest.Status = uint8(channel.StatusDeleting)
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	repairer := &LeaderRepairer{store: store, now: time.Now}

	_, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         2,
	})

	require.ErrorIs(t, err, ErrLeaderTransferInactiveChannel)
	require.Empty(t, store.upserts)
}

func TestChannelLeaderTransferReturnsLatestMetaWhenObservedEpochsAreStale(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-stale-epochs", Type: 2}
	latest := activeTransferMeta(id)
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	remote := &stubChannelLeaderRepairRemote{}
	repairer := &LeaderRepairer{store: store, remote: remote, localNode: 9, now: time.Now}

	got, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch - 1,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         2,
	})

	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, latest, got.Meta)
	require.Empty(t, store.upserts)
	require.Empty(t, remote.evaluateCalls)
}

func TestChannelLeaderTransferEvaluatesOnlyRequestedTarget(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-target-only", Type: 2}
	latest := activeTransferMeta(id)
	transferred := withTransferLeader(latest, 3)
	store := &fakeChannelMetaSource{getResults: []fakeChannelMetaGetResult{{meta: latest}, {meta: transferred}}}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
			2: {NodeID: 2, ChannelEpoch: latest.ChannelEpoch, ProjectedSafeHW: 99, CanLead: true},
			3: {NodeID: 3, ChannelEpoch: latest.ChannelEpoch, ProjectedSafeHW: 1, CanLead: true},
		},
	}
	repairer := &LeaderRepairer{store: store, remote: remote, localNode: 9, now: time.Now}

	got, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         3,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(3), got.Meta.Leader)
	require.Equal(t, []uint64{3}, evaluatedNodeIDs(remote.evaluateCalls))
}

func TestChannelLeaderTransferPersistsOnlyLeaderEpochAndLease(t *testing.T) {
	now := time.UnixMilli(1_700_002_000_000).UTC()
	id := channel.ChannelID{ID: "transfer-persist", Type: 2}
	latest := activeTransferMeta(id)
	latest.Features = 7
	latest.RetentionThroughSeq = 88
	latest.RetentionUpdatedAtMS = 1_700_001_999_000
	transferred := latest
	transferred.Leader = 2
	transferred.LeaderEpoch++
	transferred.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	store := &fakeChannelMetaSource{getResults: []fakeChannelMetaGetResult{{meta: latest}, {meta: transferred}}}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
			2: {NodeID: 2, ChannelEpoch: latest.ChannelEpoch, ProjectedSafeHW: 10, CanLead: true},
		},
	}
	var applied []metadb.ChannelRuntimeMeta
	repairer := &LeaderRepairer{
		store:     store,
		remote:    remote,
		localNode: 9,
		now:       func() time.Time { return now },
		applyAuthoritative: func(meta metadb.ChannelRuntimeMeta) error {
			applied = append(applied, meta)
			return nil
		},
	}

	got, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         2,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Len(t, store.upserts, 1)
	written := store.upserts[0]
	require.Equal(t, uint64(2), written.Leader)
	require.Equal(t, latest.LeaderEpoch+1, written.LeaderEpoch)
	require.Equal(t, now.Add(BootstrapLease).UnixMilli(), written.LeaseUntilMS)
	require.Equal(t, latest.ChannelEpoch, written.ChannelEpoch)
	require.Equal(t, latest.Replicas, written.Replicas)
	require.Equal(t, latest.ISR, written.ISR)
	require.Equal(t, latest.MinISR, written.MinISR)
	require.Equal(t, latest.Status, written.Status)
	require.Equal(t, latest.Features, written.Features)
	require.Equal(t, latest.RetentionThroughSeq, written.RetentionThroughSeq)
	require.Equal(t, latest.RetentionUpdatedAtMS, written.RetentionUpdatedAtMS)
	require.Equal(t, transferred, got.Meta)
	require.Equal(t, []metadb.ChannelRuntimeMeta{transferred}, applied)
}

func TestChannelLeaderTransferReturnsNoSafeCandidate(t *testing.T) {
	id := channel.ChannelID{ID: "transfer-unsafe", Type: 2}
	latest := activeTransferMeta(id)
	store := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest}}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
			2: {NodeID: 2, ChannelEpoch: latest.ChannelEpoch, CanLead: false, Reason: "unsafe"},
		},
	}
	repairer := &LeaderRepairer{store: store, remote: remote, localNode: 9, now: time.Now}

	_, err := repairer.TransferChannelLeaderAuthoritative(context.Background(), LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		TargetNodeID:         2,
	})

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
	require.Empty(t, store.upserts)
}

func activeTransferMeta(id channel.ChannelID) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 7,
		LeaderEpoch:  3,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       1,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: 1_700_001_000_000,
	}
}

func withTransferLeader(meta metadb.ChannelRuntimeMeta, leader uint64) metadb.ChannelRuntimeMeta {
	previous := meta.Leader
	meta.Leader = leader
	if leader != previous {
		meta.LeaderEpoch++
	}
	return meta
}
