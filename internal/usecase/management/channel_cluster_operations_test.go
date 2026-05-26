package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestGetChannelClusterReplicaDetailReportsOnlyProvenRuntimeStatus(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
			{ChannelID: "room-1", ChannelType: 2}: {
				ChannelID:    "room-1",
				ChannelType:  2,
				ChannelEpoch: 7,
				LeaderEpoch:  3,
				Leader:       1,
				Replicas:     []uint64{1, 2, 3},
				ISR:          []uint64{1, 2},
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	messages := &fakeMessageReader{
		maxSeqByChannel: map[channel.ChannelID]uint64{{ID: "room-1", Type: 2}: 42},
	}
	statusReader := &fakeChannelReplicaStatusReader{
		status: channel.ChannelRuntimeStatus{
			Leader:              1,
			HW:                  42,
			CommittedSeq:        42,
			MinAvailableSeq:     1,
			RetentionThroughSeq: 0,
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"room-1": 9},
			hashSlotForKey: map[string]uint16{"room-1": 123},
		},
		ChannelRuntimeMeta:   reader,
		Messages:             messages,
		ChannelReplicaStatus: statusReader,
	})

	got, err := app.GetChannelClusterReplicaDetail(context.Background(), "room-1", 2)
	require.NoError(t, err)
	require.Equal(t, uint16(123), got.Channel.HashSlot)
	require.True(t, got.RuntimeReported)
	require.NotNil(t, got.CommitSeq)
	require.Equal(t, uint64(42), *got.CommitSeq)
	require.NotNil(t, got.MinAvailableSeq)
	require.Equal(t, uint64(1), *got.MinAvailableSeq)
	require.NotNil(t, got.RetentionThroughSeq)
	require.Equal(t, uint64(0), *got.RetentionThroughSeq)
	require.Equal(t, []ChannelClusterReplicaStatus{
		{
			NodeID:    1,
			Role:      "leader",
			IsLeader:  true,
			InISR:     true,
			Reported:  true,
			CommitSeq: uint64Ptr(42),
			Lag:       uint64Ptr(0),
		},
		{NodeID: 2, Role: "follower", InISR: true},
		{NodeID: 3, Role: "follower"},
	}, got.Replicas)
	require.Equal(t, []channel.ChannelID{{ID: "room-1", Type: 2}}, statusReader.calls)
}

type fakeChannelReplicaStatusReader struct {
	status channel.ChannelRuntimeStatus
	err    error
	calls  []channel.ChannelID
}

func (f *fakeChannelReplicaStatusReader) ChannelRuntimeStatus(_ context.Context, id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	f.calls = append(f.calls, id)
	return f.status, f.err
}

func uint64Ptr(v uint64) *uint64 { return &v }

func TestRepairChannelClusterLeaderMapsNoLeaderToSafeRepair(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
			{ChannelID: "room-1", ChannelType: 2}: {
				ChannelID:    "room-1",
				ChannelType:  2,
				ChannelEpoch: 7,
				LeaderEpoch:  3,
				Leader:       0,
				Replicas:     []uint64{1, 2},
				ISR:          []uint64{2},
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	repair := &fakeChannelLeaderRepairOperator{
		result: RepairChannelClusterLeaderResult{
			Changed: true,
			Meta: metadb.ChannelRuntimeMeta{
				ChannelID:    "room-1",
				ChannelType:  2,
				ChannelEpoch: 7,
				LeaderEpoch:  4,
				Leader:       2,
				Replicas:     []uint64{1, 2},
				ISR:          []uint64{2},
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"room-1": 9},
			hashSlotForKey: map[string]uint16{"room-1": 123},
		},
		ChannelRuntimeMeta:  reader,
		ChannelLeaderRepair: repair,
	})

	got, err := app.RepairChannelClusterLeader(context.Background(), RepairChannelClusterLeaderRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Reason:      ChannelClusterUnhealthyReasonNoLeader,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Channel.Leader)
	require.Len(t, repair.calls, 1)
	require.Equal(t, channel.LeaderRepairReasonLeaderMissing.String(), repair.calls[0].Reason)
}

func TestRepairChannelClusterLeaderSkipsISRInsufficientOnly(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"room-1": 9},
			hashSlotForKey: map[string]uint16{"room-1": 123},
		},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{
			metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
				{ChannelID: "room-1", ChannelType: 2}: {
					ChannelID:   "room-1",
					ChannelType: 2,
					Leader:      1,
					Replicas:    []uint64{1, 2},
					ISR:         []uint64{1},
					MinISR:      2,
					Status:      uint8(channel.StatusActive),
				},
			},
		},
		ChannelLeaderRepair: &fakeChannelLeaderRepairOperator{},
	})

	_, err := app.RepairChannelClusterLeader(context.Background(), RepairChannelClusterLeaderRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Reason:      ChannelClusterUnhealthyReasonISRInsufficient,
	})

	require.ErrorIs(t, err, ErrUnsupportedChannelClusterRepairReason)
}

func TestRepairChannelClusterLeaderReturnsNoSafeCandidate(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"room-1": 9},
			hashSlotForKey: map[string]uint16{"room-1": 123},
		},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{
			metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
				{ChannelID: "room-1", ChannelType: 2}: {
					ChannelID:   "room-1",
					ChannelType: 2,
					Leader:      0,
					Replicas:    []uint64{1, 2},
					ISR:         []uint64{2},
					MinISR:      1,
					Status:      uint8(channel.StatusActive),
				},
			},
		},
		ChannelLeaderRepair: &fakeChannelLeaderRepairOperator{err: channel.ErrNoSafeChannelLeader},
	})

	_, err := app.RepairChannelClusterLeader(context.Background(), RepairChannelClusterLeaderRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Reason:      ChannelClusterUnhealthyReasonNoLeader,
	})

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
}

type fakeChannelLeaderRepairOperator struct {
	result RepairChannelClusterLeaderResult
	err    error
	calls  []RepairChannelClusterLeaderRequest
}

func (f *fakeChannelLeaderRepairOperator) RepairChannelLeader(_ context.Context, req RepairChannelClusterLeaderRequest) (RepairChannelClusterLeaderResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return RepairChannelClusterLeaderResult{}, f.err
	}
	return f.result, nil
}

func TestTransferChannelClusterLeaderValidatesTarget(t *testing.T) {
	app := New(Options{})

	tests := []TransferChannelClusterLeaderRequest{
		{ChannelType: 2, TargetNodeID: 2},
		{ChannelID: "room-1", TargetNodeID: 2},
		{ChannelID: "room-1", ChannelType: 2},
	}
	for _, req := range tests {
		_, err := app.TransferChannelClusterLeader(context.Background(), req)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
}

func TestTransferChannelClusterLeaderRejectsTargetOutsideReplicas(t *testing.T) {
	transfer := &fakeChannelLeaderTransferOperator{}
	app := newTransferTestApp(transfer, transferTestMeta("room-1", 2))

	_, err := app.TransferChannelClusterLeader(context.Background(), TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 5,
	})

	require.ErrorIs(t, err, ErrChannelLeaderTransferTargetNotReplica)
	require.Empty(t, transfer.calls)
}

func TestTransferChannelClusterLeaderRejectsTargetOutsideISR(t *testing.T) {
	meta := transferTestMeta("room-1", 2)
	meta.ISR = []uint64{1, 2}
	transfer := &fakeChannelLeaderTransferOperator{}
	app := newTransferTestApp(transfer, meta)

	_, err := app.TransferChannelClusterLeader(context.Background(), TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 3,
	})

	require.ErrorIs(t, err, ErrChannelLeaderTransferTargetNotISR)
	require.Empty(t, transfer.calls)
}

func TestTransferChannelClusterLeaderRejectsInactiveChannel(t *testing.T) {
	meta := transferTestMeta("room-1", 2)
	meta.Status = uint8(channel.StatusDeleting)
	transfer := &fakeChannelLeaderTransferOperator{}
	app := newTransferTestApp(transfer, meta)

	_, err := app.TransferChannelClusterLeader(context.Background(), TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 2,
	})

	require.ErrorIs(t, err, ErrChannelLeaderTransferInactiveChannel)
	require.Empty(t, transfer.calls)
}

func TestTransferChannelClusterLeaderReturnsUnchangedWhenTargetAlreadyLeads(t *testing.T) {
	meta := transferTestMeta("room-1", 2)
	meta.Leader = 2
	transfer := &fakeChannelLeaderTransferOperator{}
	app := newTransferTestApp(transfer, meta)

	got, err := app.TransferChannelClusterLeader(context.Background(), TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 2,
	})

	require.NoError(t, err)
	require.False(t, got.Changed)
	require.Equal(t, uint64(2), got.Channel.Leader)
	require.Empty(t, transfer.calls)
}

func TestTransferChannelClusterLeaderDelegatesSafeTransfer(t *testing.T) {
	meta := transferTestMeta("room-1", 2)
	transferred := meta
	transferred.Leader = 2
	transferred.LeaderEpoch++
	transfer := &fakeChannelLeaderTransferOperator{
		result: TransferChannelClusterLeaderResult{Changed: true, Meta: transferred},
	}
	app := newTransferTestApp(transfer, meta)

	got, err := app.TransferChannelClusterLeader(context.Background(), TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 2,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Channel.Leader)
	require.Equal(t, []TransferChannelClusterLeaderRequest{{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 2,
	}}, transfer.calls)
}

type fakeChannelLeaderTransferOperator struct {
	result TransferChannelClusterLeaderResult
	err    error
	calls  []TransferChannelClusterLeaderRequest
}

func (f *fakeChannelLeaderTransferOperator) TransferChannelLeader(_ context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return TransferChannelClusterLeaderResult{}, f.err
	}
	return f.result, nil
}

func newTransferTestApp(transfer ChannelLeaderTransferOperator, meta metadb.ChannelRuntimeMeta) *App {
	return New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{meta.ChannelID: 9},
			hashSlotForKey: map[string]uint16{meta.ChannelID: 123},
		},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{
			metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
				{ChannelID: meta.ChannelID, ChannelType: meta.ChannelType}: meta,
			},
		},
		ChannelLeaderTransfer: transfer,
	})
}

func transferTestMeta(channelID string, channelType int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 7,
		LeaderEpoch:  3,
		Leader:       1,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
	}
}
