package app

import (
	"context"
	"testing"

	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerChannelClusterStatusAdapterReturnsChannelLogStatus(t *testing.T) {
	id := channel.ChannelID{ID: "room-1", Type: 2}
	status := channel.ChannelRuntimeStatus{ID: id, Leader: 1, HW: 42, CommittedSeq: 42}
	reader := managerChannelReplicaStatusReader{channelLog: &fakeManagerChannelStatusLog{status: status}}

	got, err := reader.ChannelRuntimeStatus(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, status, got)
}

func TestManagerChannelClusterStatusAdapterPreservesNotReady(t *testing.T) {
	reader := managerChannelReplicaStatusReader{channelLog: &fakeManagerChannelStatusLog{err: channel.ErrNotReady}}

	_, err := reader.ChannelRuntimeStatus(context.Background(), channel.ChannelID{ID: "room-1", Type: 2})

	require.ErrorIs(t, err, channel.ErrNotReady)
}

func TestManagerChannelClusterRepairAdapterCallsRepairer(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2, Leader: 0}
	repaired := meta
	repaired.Leader = 2
	repairer := &fakeManagerChannelLeaderRepairer{meta: repaired, changed: true}
	operator := managerChannelLeaderRepairOperator{
		metas:    fakeManagerChannelRepairMetas{meta: meta},
		repairer: repairer,
	}

	got, err := operator.RepairChannelLeader(context.Background(), managementusecase.RepairChannelClusterLeaderRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Reason:      channel.LeaderRepairReasonLeaderMissing.String(),
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Meta.Leader)
	require.Equal(t, meta, repairer.observedMeta)
	require.Equal(t, channel.LeaderRepairReasonLeaderMissing.String(), repairer.reason)
}

func TestManagerChannelClusterTransferAdapterCallsTransferer(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2, Leader: 1, ChannelEpoch: 7, LeaderEpoch: 3}
	transferred := meta
	transferred.Leader = 2
	transferred.LeaderEpoch++
	transferer := &fakeManagerChannelLeaderTransferer{meta: transferred, changed: true}
	operator := managerChannelLeaderTransferOperator{
		metas:      fakeManagerChannelRepairMetas{meta: meta},
		transferer: transferer,
	}

	got, err := operator.TransferChannelLeader(context.Background(), managementusecase.TransferChannelClusterLeaderRequest{
		ChannelID:    "room-1",
		ChannelType:  2,
		TargetNodeID: 2,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Meta.Leader)
	require.Equal(t, meta, transferer.observedMeta)
	require.Equal(t, uint64(2), transferer.targetNodeID)
}

func TestManagerChannelClusterTransferAdapterMapsRuntimeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "target not replica", err: channelmeta.ErrLeaderTransferTargetNotReplica, want: managementusecase.ErrChannelLeaderTransferTargetNotReplica},
		{name: "target not isr", err: channelmeta.ErrLeaderTransferTargetNotISR, want: managementusecase.ErrChannelLeaderTransferTargetNotISR},
		{name: "inactive", err: channelmeta.ErrLeaderTransferInactiveChannel, want: managementusecase.ErrChannelLeaderTransferInactiveChannel},
		{name: "no safe candidate", err: channel.ErrNoSafeChannelLeader, want: channel.ErrNoSafeChannelLeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operator := managerChannelLeaderTransferOperator{
				metas:      fakeManagerChannelRepairMetas{meta: metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2}},
				transferer: &fakeManagerChannelLeaderTransferer{err: tt.err},
			}

			_, err := operator.TransferChannelLeader(context.Background(), managementusecase.TransferChannelClusterLeaderRequest{
				ChannelID:    "room-1",
				ChannelType:  2,
				TargetNodeID: 2,
			})

			require.ErrorIs(t, err, tt.want)
		})
	}
}

type fakeManagerChannelStatusLog struct {
	status channel.ChannelRuntimeStatus
	err    error
}

func (f *fakeManagerChannelStatusLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return f.status, f.err
}

type fakeManagerChannelRepairMetas struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f fakeManagerChannelRepairMetas) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return f.meta, f.err
}

type fakeManagerChannelLeaderRepairer struct {
	meta         metadb.ChannelRuntimeMeta
	changed      bool
	err          error
	observedMeta metadb.ChannelRuntimeMeta
	reason       string
}

func (f *fakeManagerChannelLeaderRepairer) RepairIfNeeded(_ context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error) {
	f.observedMeta = meta
	f.reason = reason
	if f.err != nil {
		return metadb.ChannelRuntimeMeta{}, false, f.err
	}
	return f.meta, f.changed, nil
}

type fakeManagerChannelLeaderTransferer struct {
	meta         metadb.ChannelRuntimeMeta
	changed      bool
	err          error
	observedMeta metadb.ChannelRuntimeMeta
	targetNodeID uint64
}

func (f *fakeManagerChannelLeaderTransferer) TransferIfSafe(_ context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error) {
	f.observedMeta = meta
	f.targetNodeID = targetNodeID
	if f.err != nil {
		return metadb.ChannelRuntimeMeta{}, false, f.err
	}
	return f.meta, f.changed, nil
}
