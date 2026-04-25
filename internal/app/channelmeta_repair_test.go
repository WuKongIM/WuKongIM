package app

import (
	"context"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestChannelMetaSyncRefreshRepairsDeadLeaderViaSlotLeader(t *testing.T) {
	id := channel.ChannelID{ID: "repair-refresh", Type: 1}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: {
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 9,
				LeaderEpoch:  7,
				Replicas:     []uint64{2, 3, 4},
				ISR:          []uint64{2, 3, 4},
				Leader:       3,
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	cluster := &fakeChannelMetaCluster{}
	repairer := &stubChannelMetaRepairer{
		meta: metadb.ChannelRuntimeMeta{
			ChannelID:    id.ID,
			ChannelType:  int64(id.Type),
			ChannelEpoch: 9,
			LeaderEpoch:  8,
			Replicas:     []uint64{2, 3, 4},
			ISR:          []uint64{2, 3, 4},
			Leader:       4,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
		},
		changed: true,
	}
	syncer := &channelMetaSync{
		source:    source,
		cluster:   cluster,
		localNode: 2,
		repairer:  repairer,
	}
	syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDead)

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, channel.NodeID(4), got.Leader)
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_dead", repairer.calls[0].reason)
}

func TestChannelMetaSyncRefreshRepairsExpiredLeaderLeaseViaSlotLeader(t *testing.T) {
	id := channel.ChannelID{ID: "repair-expired-lease", Type: 1}
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3, 4},
		ISR:          []uint64{2, 3, 4},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(-time.Second).UnixMilli(),
	}
	repaired := expired
	repaired.Leader = 4
	repaired.LeaderEpoch = 8
	repaired.LeaseUntilMS = time.Now().Add(time.Minute).UnixMilli()
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			id: expired,
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
		localNode: 2,
		repairer:  repairer,
	}

	got, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, channel.NodeID(4), got.Leader)
	require.Len(t, repairer.calls, 1)
	require.Equal(t, "leader_lease_expired", repairer.calls[0].reason)
}

func TestChannelMetaSyncRefreshReturnsErrNoSafeChannelLeaderWhenRepairHasNoCandidate(t *testing.T) {
	id := channel.ChannelID{ID: "repair-none", Type: 1}
	source := &fakeChannelMetaSource{
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
	syncer := &channelMetaSync{
		source:    source,
		cluster:   &fakeChannelMetaCluster{},
		localNode: 2,
		repairer:  &stubChannelMetaRepairer{err: channel.ErrNoSafeChannelLeader},
	}
	syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDead)

	_, err := syncer.RefreshChannelMeta(context.Background(), id)

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
}

func TestChannelMetaSyncRefreshSingleflightsConcurrentLeaderRepair(t *testing.T) {
	id := channel.ChannelID{ID: "repair-sf", Type: 1}
	source := &fakeChannelMetaSource{
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
	repairer := &stubChannelMetaRepairer{
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
	syncer := &channelMetaSync{
		source:    source,
		cluster:   &fakeChannelMetaCluster{},
		localNode: 2,
		repairer:  repairer,
	}
	syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDead)

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

	require.Len(t, repairer.calls, 1)
}

func TestChannelLeaderRepairerRereadsAuthoritativeMetaBeforeChoosingCandidate(t *testing.T) {
	id := channel.ChannelID{ID: "repair-reread", Type: 1}
	latest := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 7,
		LeaderEpoch:  9,
		Replicas:     []uint64{2, 4},
		ISR:          []uint64{2, 4},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest},
	}
	remote := &stubChannelLeaderRepairRemote{}
	repairer := &channelLeaderRepairer{
		store: source,
		now:   time.Now,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 3, "leader_dead"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: 7,
		ObservedLeaderEpoch:  8,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, latest, got.Meta)
	require.Empty(t, remote.evaluateCalls)
}

func TestChannelLeaderRepairerSelectsCandidateWithHighestProjectedSafeHW(t *testing.T) {
	now := time.UnixMilli(1_700_000_999_000).UTC()
	id := channel.ChannelID{ID: "repair-pick", Type: 1}
	dead := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{2, 4, 5},
		Leader:       5,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	repaired := dead
	repaired.Leader = 4
	repaired.LeaderEpoch = 8
	repaired.LeaseUntilMS = now.Add(channelMetaBootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: dead},
			{meta: repaired},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			2: {
				NodeID:              2,
				Exists:              true,
				ChannelEpoch:        dead.ChannelEpoch,
				LocalLEO:            10,
				LocalCheckpointHW:   8,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     8,
				ProjectedTruncateTo: 8,
				CanLead:             true,
			},
			4: {
				NodeID:              4,
				Exists:              true,
				ChannelEpoch:        dead.ChannelEpoch,
				LocalLEO:            12,
				LocalCheckpointHW:   9,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 5, "leader_dead"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: dead.ChannelEpoch,
		ObservedLeaderEpoch:  dead.LeaderEpoch,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(4), got.Meta.Leader)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(4), source.upserts[0].Leader)
	require.Equal(t, dead.LeaderEpoch+1, source.upserts[0].LeaderEpoch)
	require.Equal(t, dead.Replicas, source.upserts[0].Replicas)
	require.Equal(t, dead.ISR, source.upserts[0].ISR)
	require.Equal(t, now.Add(channelMetaBootstrapLease).UnixMilli(), source.upserts[0].LeaseUntilMS)
}

func TestChannelLeaderRepairerKeepsExpiredLeaderCandidateDuringRepair(t *testing.T) {
	now := time.UnixMilli(1_700_001_111_000).UTC()
	id := channel.ChannelID{ID: "repair-expired-skip", Type: 1}
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4},
		ISR:          []uint64{4, 2},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
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
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			4: {
				NodeID:              4,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            11,
				LocalCheckpointHW:   11,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
			2: {
				NodeID:              2,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            10,
				LocalCheckpointHW:   10,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     10,
				ProjectedTruncateTo: 10,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: expired.ChannelEpoch,
		ObservedLeaderEpoch:  expired.LeaderEpoch,
		Reason:               "leader_lease_expired",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Empty(t, remote.evaluateCalls)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(4), source.upserts[0].Leader)
	require.Equal(t, expired.LeaderEpoch, source.upserts[0].LeaderEpoch)
}

func TestChannelLeaderRepairerKeepsExpiredLeaderInEvaluationMeta(t *testing.T) {
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    "repair-expired-meta",
		ChannelType:  1,
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{4, 2, 5},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	require.Equal(t, []uint64{4, 2, 5}, evaluationMetaForRepair(expired, "leader_lease_expired").ISR)
}

func TestChannelLeaderRepairerRenewsExpiredLeaderLeaseWithoutLeaderTransferWhenCurrentLeaderStillSafe(t *testing.T) {
	now := time.UnixMilli(1_700_001_112_500).UTC()
	id := channel.ChannelID{ID: "repair-expired-renew", Type: 1}
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{4, 2, 5},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
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
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			4: {
				NodeID:              4,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            12,
				LocalCheckpointHW:   11,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
			2: {
				NodeID:              2,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            10,
				LocalCheckpointHW:   10,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     10,
				ProjectedTruncateTo: 10,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: expired.ChannelEpoch,
		ObservedLeaderEpoch:  expired.LeaderEpoch,
		Reason:               "leader_lease_expired",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(4), source.upserts[0].Leader)
	require.Equal(t, expired.LeaderEpoch, source.upserts[0].LeaderEpoch)
	require.Equal(t, now.Add(channelMetaBootstrapLease).UnixMilli(), source.upserts[0].LeaseUntilMS)
	require.Empty(t, remote.evaluateCalls)
}

func TestChannelLeaderRepairerKeepsCurrentLeaderOnExpiredLeaseTie(t *testing.T) {
	now := time.UnixMilli(1_700_001_112_900).UTC()
	id := channel.ChannelID{ID: "repair-expired-tie", Type: 1}
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{1, 3},
		ISR:          []uint64{3, 1},
		Leader:       3,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
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
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			3: {
				NodeID:              3,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            10,
				LocalCheckpointHW:   10,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     10,
				ProjectedTruncateTo: 10,
				CanLead:             true,
			},
			1: {
				NodeID:              1,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            10,
				LocalCheckpointHW:   10,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     10,
				ProjectedTruncateTo: 10,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 3, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: expired.ChannelEpoch,
		ObservedLeaderEpoch:  expired.LeaderEpoch,
		Reason:               "leader_lease_expired",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(3), source.upserts[0].Leader)
	require.Equal(t, expired.LeaderEpoch, source.upserts[0].LeaderEpoch)
	require.Empty(t, remote.evaluateCalls)
}

func TestChannelLeaderRepairerAppliesAuthoritativeMetaLocallyAfterRepair(t *testing.T) {
	now := time.UnixMilli(1_700_001_113_000).UTC()
	id := channel.ChannelID{ID: "repair-apply-local", Type: 1}
	dead := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{2, 4, 5},
		Leader:       5,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	repaired := dead
	repaired.Leader = 4
	repaired.LeaderEpoch = 8
	repaired.LeaseUntilMS = now.Add(channelMetaBootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: dead},
			{meta: repaired},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			4: {
				NodeID:              4,
				Exists:              true,
				ChannelEpoch:        dead.ChannelEpoch,
				LocalLEO:            12,
				LocalCheckpointHW:   11,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
		},
	}
	var applied []metadb.ChannelRuntimeMeta
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 5, "leader_dead"
		},
		remote: remote,
		applyAuthoritative: func(meta metadb.ChannelRuntimeMeta) error {
			applied = append(applied, meta)
			return nil
		},
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: dead.ChannelEpoch,
		ObservedLeaderEpoch:  dead.LeaderEpoch,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Len(t, applied, 1)
	require.Equal(t, repaired, applied[0])
}

func TestChannelLeaderRepairerRoutesRepairToRemoteSlotLeader(t *testing.T) {
	id := channel.ChannelID{ID: "repair-remote", Type: 1}
	observed := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 12,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{2, 4, 5},
		Leader:       5,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	repaired := observed
	repaired.Leader = 4
	repaired.LeaderEpoch = 8
	remote := &stubChannelLeaderRepairRemote{
		repairResult: accessnode.ChannelLeaderRepairResult{
			Meta:    repaired,
			Changed: true,
		},
	}
	repairer := &channelLeaderRepairer{
		cluster: fakeChannelLeaderRepairCluster{
			slotID:   7,
			leaderID: 9,
		},
		remote: remote,
	}

	got, changed, err := repairer.RepairIfNeeded(context.Background(), observed, "leader_dead")

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, repaired, got)
	require.Len(t, remote.repairCalls, 1)
	require.Equal(t, accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: observed.ChannelEpoch,
		ObservedLeaderEpoch:  observed.LeaderEpoch,
		Reason:               "leader_dead",
	}, remote.repairCalls[0])
}

func TestChannelLeaderRepairerReroutesWhenLocalSlotLeadershipChangesBeforePersist(t *testing.T) {
	id := channel.ChannelID{ID: "repair-reroute", Type: 1}
	observed := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 12,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{2, 4, 5},
		Leader:       5,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	repaired := observed
	repaired.Leader = 4
	repaired.LeaderEpoch++
	source := &fakeChannelMetaSource{
		get:       map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: observed},
		upsertErr: raftcluster.ErrNotLeader,
	}
	remote := &stubChannelLeaderRepairRemote{
		repairResult: accessnode.ChannelLeaderRepairResult{
			Meta:    repaired,
			Changed: true,
		},
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			2: {
				NodeID:       2,
				ChannelEpoch: observed.ChannelEpoch,
				CanLead:      true,
			},
			4: {
				NodeID:              4,
				ChannelEpoch:        observed.ChannelEpoch,
				ProjectedSafeHW:     9,
				ProjectedTruncateTo: 9,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		cluster:   &sequencedChannelLeaderRepairCluster{leaders: []multiraft.NodeID{2, 9}, localLeader: 2},
		remote:    remote,
		localNode: 99,
		now:       time.Now,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 5, "leader_dead"
		},
	}

	got, changed, err := repairer.RepairIfNeeded(context.Background(), observed, "leader_dead")

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, repaired, got)
	require.Len(t, source.upserts, 1)
	require.Len(t, remote.repairCalls, 1)
}

func TestChannelLeaderRepairerUsesLatestRepairReasonBeforeSelectingCandidate(t *testing.T) {
	id := channel.ChannelID{ID: "repair-latest-reason", Type: 1}
	latest := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 14,
		LeaderEpoch:  8,
		Replicas:     []uint64{2, 4},
		ISR:          []uint64{2, 4},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: latest},
			{meta: latest},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			2: {
				NodeID:              2,
				ChannelEpoch:        latest.ChannelEpoch,
				ProjectedSafeHW:     8,
				ProjectedTruncateTo: 8,
				CanLead:             true,
			},
			4: {
				NodeID:              4,
				ChannelEpoch:        latest.ChannelEpoch,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 99,
		now:       time.Now,
		remote:    remote,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_draining"
		},
	}

	_, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch,
		ObservedLeaderEpoch:  latest.LeaderEpoch,
		Reason:               "leader_missing",
	})

	require.NoError(t, err)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(2), source.upserts[0].Leader)
}

func TestChannelLeaderRepairerReturnsLatestMetaWhenObservedEpochsAreStale(t *testing.T) {
	id := channel.ChannelID{ID: "repair-stale-epochs", Type: 1}
	latest := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 14,
		LeaderEpoch:  9,
		Replicas:     []uint64{2, 4},
		ISR:          []uint64{2, 4},
		Leader:       4,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	source := &fakeChannelMetaSource{
		get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: latest},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]accessnode.ChannelLeaderPromotionReport{
			2: {
				NodeID:              2,
				ChannelEpoch:        latest.ChannelEpoch,
				ProjectedSafeHW:     8,
				ProjectedTruncateTo: 8,
				CanLead:             true,
			},
		},
	}
	repairer := &channelLeaderRepairer{
		store:     source,
		localNode: 99,
		now:       time.Now,
		remote:    remote,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_dead"
		},
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), accessnode.ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: latest.ChannelEpoch - 1,
		ObservedLeaderEpoch:  latest.LeaderEpoch - 1,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, latest, got.Meta)
	require.Empty(t, remote.evaluateCalls)
	require.Empty(t, source.upserts)
}

func TestChannelLeaderPromotionEvaluatorCollectPromotionProofsValidatesProbeResponses(t *testing.T) {
	meta := channel.Meta{
		Key:   channel.ChannelKey("channel/1/ZGVhZC1wcm9vZg=="),
		ID:    channel.ChannelID{ID: "dead-proof", Type: 1},
		Epoch: 11,
		ISR:   []channel.NodeID{2, 3, 4},
	}
	probe := &stubChannelLeaderProbeClient{
		responses: map[channel.NodeID]channelruntime.ReconcileProbeResponseEnvelope{
			3: {
				ChannelKey:   meta.Key,
				Epoch:        meta.Epoch,
				Generation:   9,
				ReplicaID:    99,
				OffsetEpoch:  7,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
			4: {
				ChannelKey:   channel.ChannelKey("channel/1/bWlzbWF0Y2g="),
				Epoch:        meta.Epoch,
				Generation:   0,
				ReplicaID:    4,
				OffsetEpoch:  7,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
		},
	}
	evaluator := &channelLeaderPromotionEvaluator{
		localNode: 2,
		probe:     probe,
	}

	proofs := evaluator.collectPromotionProofs(context.Background(), meta)

	require.Len(t, proofs, 1)
	require.Equal(t, meta.Key, proofs[0].ChannelKey)
	require.Equal(t, meta.Epoch, proofs[0].Epoch)
	require.Equal(t, channel.NodeID(3), proofs[0].ReplicaID)
}

func TestValidPromotionProbeResponseRequiresGenerationMatchWhenPinned(t *testing.T) {
	req := channelruntime.ReconcileProbeRequestEnvelope{
		ChannelKey: channel.ChannelKey("channel/1/cGlubmVk"),
		Epoch:      5,
		Generation: 7,
	}
	require.True(t, validPromotionProbeResponse(req, channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey: req.ChannelKey,
		Epoch:      req.Epoch,
		Generation: req.Generation,
	}))
	require.False(t, validPromotionProbeResponse(req, channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey: req.ChannelKey,
		Epoch:      req.Epoch,
		Generation: req.Generation + 1,
	}))
}

type stubChannelMetaRepairer struct {
	calls   []stubChannelMetaRepairCall
	meta    metadb.ChannelRuntimeMeta
	changed bool
	err     error
	block   <-chan struct{}
}

type stubChannelMetaRepairCall struct {
	meta   metadb.ChannelRuntimeMeta
	reason string
}

func (s *stubChannelMetaRepairer) RepairIfNeeded(_ context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error) {
	s.calls = append(s.calls, stubChannelMetaRepairCall{meta: meta, reason: reason})
	if s.block != nil {
		<-s.block
	}
	return s.meta, s.changed, s.err
}

type stubChannelLeaderRepairRemote struct {
	repairCalls       []accessnode.ChannelLeaderRepairRequest
	repairResult      accessnode.ChannelLeaderRepairResult
	repairErr         error
	evaluateCalls     []stubChannelLeaderEvaluateCall
	evaluateByNode    map[uint64]accessnode.ChannelLeaderPromotionReport
	evaluateErrByNode map[uint64]error
}

type stubChannelLeaderEvaluateCall struct {
	nodeID uint64
	req    accessnode.ChannelLeaderEvaluateRequest
}

func (s *stubChannelLeaderRepairRemote) RepairChannelLeader(_ context.Context, req accessnode.ChannelLeaderRepairRequest) (accessnode.ChannelLeaderRepairResult, error) {
	s.repairCalls = append(s.repairCalls, req)
	return s.repairResult, s.repairErr
}

func (s *stubChannelLeaderRepairRemote) EvaluateChannelLeaderCandidate(_ context.Context, nodeID uint64, req accessnode.ChannelLeaderEvaluateRequest) (accessnode.ChannelLeaderPromotionReport, error) {
	s.evaluateCalls = append(s.evaluateCalls, stubChannelLeaderEvaluateCall{nodeID: nodeID, req: req})
	if err := s.evaluateErrByNode[nodeID]; err != nil {
		return accessnode.ChannelLeaderPromotionReport{}, err
	}
	return s.evaluateByNode[nodeID], nil
}

type fakeChannelLeaderRepairCluster struct {
	slotID   multiraft.SlotID
	leaderID multiraft.NodeID
}

func (f fakeChannelLeaderRepairCluster) SlotForKey(string) multiraft.SlotID {
	return f.slotID
}

func (f fakeChannelLeaderRepairCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return f.leaderID, nil
}

func (f fakeChannelLeaderRepairCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return false
}

type sequencedChannelLeaderRepairCluster struct {
	mu          sync.Mutex
	leaders     []multiraft.NodeID
	localLeader multiraft.NodeID
}

func (s *sequencedChannelLeaderRepairCluster) SlotForKey(string) multiraft.SlotID {
	return 7
}

func (s *sequencedChannelLeaderRepairCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.leaders) == 0 {
		return 0, nil
	}
	leader := s.leaders[0]
	if len(s.leaders) > 1 {
		s.leaders = s.leaders[1:]
	}
	return leader, nil
}

func (s *sequencedChannelLeaderRepairCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return nodeID == s.localLeader
}

type stubChannelLeaderProbeClient struct {
	responses map[channel.NodeID]channelruntime.ReconcileProbeResponseEnvelope
	errs      map[channel.NodeID]error
}

func (s *stubChannelLeaderProbeClient) Probe(_ context.Context, peer channel.NodeID, _ channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error) {
	if err := s.errs[peer]; err != nil {
		return channelruntime.ReconcileProbeResponseEnvelope{}, err
	}
	return s.responses[peer], nil
}
