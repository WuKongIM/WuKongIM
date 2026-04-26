package channelmeta

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

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
	repairer := &LeaderRepairer{
		store: source,
		now:   time.Now,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 3, "leader_dead"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
	repaired.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: dead},
			{meta: repaired},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 5, "leader_dead"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
	require.Equal(t, now.Add(BootstrapLease).UnixMilli(), source.upserts[0].LeaseUntilMS)
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
	renewed.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: expired},
			{meta: renewed},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: expired.ChannelEpoch,
		ObservedLeaderEpoch:  expired.LeaderEpoch,
		Reason:               "leader_lease_expired",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, []uint64{4}, evaluatedNodeIDs(remote.evaluateCalls))
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
	renewed.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: expired},
			{meta: renewed},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
	require.Equal(t, now.Add(BootstrapLease).UnixMilli(), source.upserts[0].LeaseUntilMS)
	require.Equal(t, []uint64{4}, evaluatedNodeIDs(remote.evaluateCalls))
}

func TestChannelLeaderRepairerTransfersExpiredLeaseWhenCurrentLeaderCannotBeEvaluated(t *testing.T) {
	now := time.UnixMilli(1_700_001_112_700).UTC()
	id := channel.ChannelID{ID: "repair-expired-unreachable", Type: 1}
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
	repaired := expired
	repaired.Leader = 2
	repaired.LeaderEpoch = expired.LeaderEpoch + 1
	repaired.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: expired},
			{meta: repaired},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateErrByNode: map[uint64]error{
			4: context.DeadlineExceeded,
		},
		evaluateByNode: map[uint64]LeaderPromotionReport{
			2: {
				NodeID:              2,
				Exists:              true,
				ChannelEpoch:        expired.ChannelEpoch,
				LocalLEO:            11,
				LocalCheckpointHW:   11,
				LocalOffsetEpoch:    4,
				ProjectedSafeHW:     11,
				ProjectedTruncateTo: 11,
				CanLead:             true,
			},
			5: {
				NodeID:              5,
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: expired.ChannelEpoch,
		ObservedLeaderEpoch:  expired.LeaderEpoch,
		Reason:               "leader_lease_expired",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Len(t, source.upserts, 1)
	require.Equal(t, uint64(2), source.upserts[0].Leader)
	require.Equal(t, expired.LeaderEpoch+1, source.upserts[0].LeaderEpoch)
	require.Equal(t, []uint64{4, 2, 5}, evaluatedNodeIDs(remote.evaluateCalls))
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
	renewed.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: expired},
			{meta: renewed},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 9,
		now:       func() time.Time { return now },
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 3, "leader_lease_expired"
		},
		remote: remote,
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
	require.Equal(t, []uint64{3}, evaluatedNodeIDs(remote.evaluateCalls))
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
	repaired.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	source := &fakeChannelMetaSource{
		getResults: []fakeChannelMetaGetResult{
			{meta: dead},
			{meta: repaired},
		},
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
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

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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

func TestChannelLeaderRepairerEvaluatesDeadLeaderCandidateAsProjectedLeader(t *testing.T) {
	dead := metadb.ChannelRuntimeMeta{
		ChannelID:    "repair-candidate-projected-leader",
		ChannelType:  1,
		ChannelEpoch: 11,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 4, 5},
		ISR:          []uint64{2, 4, 5},
		Leader:       5,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
	remote := &stubChannelLeaderRepairRemote{
		evaluateByNode: map[uint64]LeaderPromotionReport{
			4: {
				NodeID:       4,
				ChannelEpoch: dead.ChannelEpoch,
				CanLead:      true,
			},
		},
	}
	repairer := &LeaderRepairer{
		remote:    remote,
		localNode: 9,
	}

	_, err := repairer.evaluateLeaderCandidate(context.Background(), 4, dead, "leader_dead")

	require.NoError(t, err)
	require.Len(t, remote.evaluateCalls, 1)
	gotMeta := remote.evaluateCalls[0].req.Meta
	require.Equal(t, uint64(4), gotMeta.Leader)
	require.Equal(t, []uint64{2, 4}, gotMeta.ISR)
	require.Equal(t, dead.LeaderEpoch, gotMeta.LeaderEpoch)
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
		repairResult: LeaderRepairResult{
			Meta:    repaired,
			Changed: true,
		},
	}
	repairer := &LeaderRepairer{
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
	require.Equal(t, LeaderRepairRequest{
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
		repairResult: LeaderRepairResult{
			Meta:    repaired,
			Changed: true,
		},
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
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
		evaluateByNode: map[uint64]LeaderPromotionReport{
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
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 99,
		now:       time.Now,
		remote:    remote,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_draining"
		},
	}

	_, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
		evaluateByNode: map[uint64]LeaderPromotionReport{
			2: {
				NodeID:              2,
				ChannelEpoch:        latest.ChannelEpoch,
				ProjectedSafeHW:     8,
				ProjectedTruncateTo: 8,
				CanLead:             true,
			},
		},
	}
	repairer := &LeaderRepairer{
		store:     source,
		localNode: 99,
		now:       time.Now,
		remote:    remote,
		needsRepair: func(meta metadb.ChannelRuntimeMeta) (bool, string) {
			return meta.Leader == 4, "leader_dead"
		},
	}

	got, err := repairer.RepairChannelLeaderAuthoritative(context.Background(), LeaderRepairRequest{
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
				LeaderEpoch:  meta.LeaderEpoch,
				Generation:   9,
				ReplicaID:    3,
				OffsetEpoch:  7,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
			4: {
				ChannelKey:   channel.ChannelKey("channel/1/bWlzbWF0Y2g="),
				Epoch:        meta.Epoch,
				LeaderEpoch:  meta.LeaderEpoch,
				Generation:   0,
				ReplicaID:    4,
				OffsetEpoch:  7,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
		},
	}
	evaluator := &LeaderPromotionEvaluator{
		localNode: 2,
		probe:     probe,
	}

	proofs := evaluator.collectPromotionProofs(context.Background(), meta)

	require.Len(t, proofs, 1)
	require.Equal(t, meta.Key, proofs[0].ChannelKey)
	require.Equal(t, meta.Epoch, proofs[0].Epoch)
	require.Equal(t, meta.LeaderEpoch, proofs[0].LeaderEpoch)
	require.Equal(t, channel.NodeID(3), proofs[0].ReplicaID)
}

func TestPromotionProofsRejectStaleLeaderEpoch(t *testing.T) {
	meta := ProjectChannelMeta(metadb.ChannelRuntimeMeta{
		ChannelID:    "promotion-stale-leader-epoch",
		ChannelType:  1,
		ChannelEpoch: 9,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	})
	probe := &stubChannelLeaderProbeClient{
		responses: map[channel.NodeID]channelruntime.ReconcileProbeResponseEnvelope{
			3: {
				ChannelKey:   meta.Key,
				Epoch:        meta.Epoch,
				LeaderEpoch:  meta.LeaderEpoch - 1,
				Generation:   0,
				ReplicaID:    3,
				OffsetEpoch:  9,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
		},
	}
	evaluator := &LeaderPromotionEvaluator{
		localNode: 2,
		probe:     probe,
	}

	proofs := evaluator.collectPromotionProofs(context.Background(), meta)

	require.Empty(t, proofs)
	require.Len(t, probe.requests, 1)
	require.Equal(t, meta.LeaderEpoch, probe.requests[0].LeaderEpoch)
}

func TestPromotionProofsRejectMismatchedReplicaID(t *testing.T) {
	meta := ProjectChannelMeta(metadb.ChannelRuntimeMeta{
		ChannelID:    "promotion-wrong-peer",
		ChannelType:  1,
		ChannelEpoch: 9,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	})
	probe := &stubChannelLeaderProbeClient{
		responses: map[channel.NodeID]channelruntime.ReconcileProbeResponseEnvelope{
			3: {
				ChannelKey:   meta.Key,
				Epoch:        meta.Epoch,
				LeaderEpoch:  meta.LeaderEpoch,
				Generation:   0,
				ReplicaID:    4,
				OffsetEpoch:  9,
				LogEndOffset: 12,
				CheckpointHW: 10,
			},
		},
	}
	evaluator := &LeaderPromotionEvaluator{
		localNode: 2,
		probe:     probe,
	}

	proofs := evaluator.collectPromotionProofs(context.Background(), meta)

	require.Empty(t, proofs)
}

func TestValidPromotionProbeResponseRequiresGenerationMatchWhenPinned(t *testing.T) {
	req := channelruntime.ReconcileProbeRequestEnvelope{
		ChannelKey:  channel.ChannelKey("channel/1/cGlubmVk"),
		Epoch:       5,
		LeaderEpoch: 11,
		Generation:  7,
	}
	require.True(t, validPromotionProbeResponse(req, channel.NodeID(3), channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch,
		Generation:  req.Generation,
		ReplicaID:   3,
	}))
	require.False(t, validPromotionProbeResponse(req, channel.NodeID(3), channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch,
		Generation:  req.Generation + 1,
		ReplicaID:   3,
	}))
	require.False(t, validPromotionProbeResponse(req, channel.NodeID(3), channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch - 1,
		Generation:  req.Generation,
		ReplicaID:   3,
	}))
	require.False(t, validPromotionProbeResponse(req, channel.NodeID(3), channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch,
		Generation:  req.Generation,
		ReplicaID:   4,
	}))
}

type fakeChannelMetaSource struct {
	mu         sync.Mutex
	get        map[channel.ChannelID]metadb.ChannelRuntimeMeta
	getResults []fakeChannelMetaGetResult
	getCalls   int
	upsertErr  error
	upserts    []metadb.ChannelRuntimeMeta
}

type fakeChannelMetaGetResult struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f *fakeChannelMetaSource) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getCalls <= len(f.getResults) {
		result := f.getResults[f.getCalls-1]
		if result.err != nil {
			return metadb.ChannelRuntimeMeta{}, result.err
		}
		return result.meta, nil
	}
	return f.get[channel.ChannelID{ID: channelID, Type: uint8(channelType)}], nil
}

func (f *fakeChannelMetaSource) UpsertChannelRuntimeMetaIfLocalLeader(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

type stubChannelLeaderRepairRemote struct {
	repairCalls       []LeaderRepairRequest
	repairResult      LeaderRepairResult
	repairErr         error
	evaluateCalls     []stubChannelLeaderEvaluateCall
	evaluateByNode    map[uint64]LeaderPromotionReport
	evaluateErrByNode map[uint64]error
}

type stubChannelLeaderEvaluateCall struct {
	nodeID uint64
	req    LeaderEvaluateRequest
}

func evaluatedNodeIDs(calls []stubChannelLeaderEvaluateCall) []uint64 {
	out := make([]uint64, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.nodeID)
	}
	return out
}

func (s *stubChannelLeaderRepairRemote) RepairChannelLeader(_ context.Context, req LeaderRepairRequest) (LeaderRepairResult, error) {
	s.repairCalls = append(s.repairCalls, req)
	return s.repairResult, s.repairErr
}

func (s *stubChannelLeaderRepairRemote) EvaluateChannelLeaderCandidate(_ context.Context, nodeID uint64, req LeaderEvaluateRequest) (LeaderPromotionReport, error) {
	s.evaluateCalls = append(s.evaluateCalls, stubChannelLeaderEvaluateCall{nodeID: nodeID, req: req})
	if err := s.evaluateErrByNode[nodeID]; err != nil {
		return LeaderPromotionReport{}, err
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
	requests  []channelruntime.ReconcileProbeRequestEnvelope
}

func (s *stubChannelLeaderProbeClient) Probe(_ context.Context, peer channel.NodeID, req channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error) {
	s.requests = append(s.requests, req)
	if err := s.errs[peer]; err != nil {
		return channelruntime.ReconcileProbeResponseEnvelope{}, err
	}
	return s.responses[peer], nil
}
