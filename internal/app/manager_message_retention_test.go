package app

import (
	"context"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerMessageRetentionAdvancesLocalLeader(t *testing.T) {
	id := channel.ChannelID{ID: "retain-local", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	now := time.Unix(2000, 0)
	runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{
		managerRetentionView(key, 0, 10, 10, 10, now),
		managerRetentionView(key, 0, 10, 10, 10, now),
	}}
	metadata := &fakeManagerRetentionMetadata{}
	stores := &fakeManagerRetentionStores{cursor: 10, cursorExists: true}

	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas: managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Leader:      1,
		}},
		runtime:  runtime,
		stores:   stores,
		metadata: metadata,
		now:      func() time.Time { return now },
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  9,
	})

	require.NoError(t, err)
	require.Equal(t, managementusecase.MessageRetentionStatusAdvanced, got.Status)
	require.Equal(t, uint64(9), got.AdvancedThroughSeq)
	require.Equal(t, uint64(10), got.MinAvailableSeq)
	require.Equal(t, uint64(9), metadata.lastReq.RetentionThroughSeq)
	require.Equal(t, []uint64{9}, runtime.applied)
	require.Equal(t, uint64(1), metadata.lastReq.ExpectedLeader)
	require.Equal(t, uint64(7), metadata.lastReq.ExpectedChannelEpoch)
	require.Equal(t, uint64(8), metadata.lastReq.ExpectedLeaderEpoch)
}

func TestManagerMessageRetentionDryRunDoesNotMutate(t *testing.T) {
	id := channel.ChannelID{ID: "retain-dry", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	now := time.Unix(2000, 0)
	runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{managerRetentionView(key, 0, 10, 10, 10, now)}}
	metadata := &fakeManagerRetentionMetadata{}
	stores := &fakeManagerRetentionStores{cursor: 10, cursorExists: true}

	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas:       managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}},
		runtime:     runtime,
		stores:      stores,
		metadata:    metadata,
		now:         func() time.Time { return now },
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  9,
		DryRun:      true,
	})

	require.NoError(t, err)
	require.Equal(t, managementusecase.MessageRetentionStatusWouldAdvance, got.Status)
	require.Equal(t, uint64(9), got.AdvancedThroughSeq)
	require.Zero(t, metadata.calls)
	require.Empty(t, runtime.applied)
	require.Equal(t, 1, stores.loadCalls)
	require.Zero(t, stores.confirmCalls)
}

func TestManagerMessageRetentionClampsToSafetyGate(t *testing.T) {
	id := channel.ChannelID{ID: "retain-clamp", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	now := time.Unix(2000, 0)
	runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{
		managerRetentionView(key, 4, 20, 8, 20, now),
		managerRetentionView(key, 4, 20, 8, 20, now),
	}}
	metadata := &fakeManagerRetentionMetadata{}

	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas:       managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}},
		runtime:     runtime,
		stores:      &fakeManagerRetentionStores{cursor: 20, cursorExists: true},
		metadata:    metadata,
		now:         func() time.Time { return now },
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  15,
	})

	require.NoError(t, err)
	require.Equal(t, managementusecase.MessageRetentionStatusAdvanced, got.Status)
	require.Equal(t, uint64(8), got.AdvancedThroughSeq)
	require.Equal(t, uint64(8), metadata.lastReq.RetentionThroughSeq)
	require.Equal(t, []uint64{8}, runtime.applied)
}

func TestManagerMessageRetentionBlockedByReplayCursor(t *testing.T) {
	id := channel.ChannelID{ID: "retain-blocked", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	now := time.Unix(2000, 0)
	runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{
		managerRetentionView(key, 4, 20, 20, 20, now),
		managerRetentionView(key, 4, 20, 20, 20, now),
	}}
	metadata := &fakeManagerRetentionMetadata{}

	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas:       managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}},
		runtime:     runtime,
		stores:      &fakeManagerRetentionStores{cursor: 4, cursorExists: true},
		metadata:    metadata,
		now:         func() time.Time { return now },
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  10,
	})

	require.NoError(t, err)
	require.Equal(t, managementusecase.MessageRetentionStatusBlocked, got.Status)
	require.Equal(t, managementusecase.MessageRetentionBlockedReasonReplayCursor, got.BlockedReason)
	require.Equal(t, uint64(4), got.AdvancedThroughSeq)
	require.Zero(t, metadata.calls)
	require.Empty(t, runtime.applied)
}

func TestManagerMessageRetentionNoopWhenRequestDoesNotExceedCurrentBoundary(t *testing.T) {
	id := channel.ChannelID{ID: "retain-noop", Type: 2}
	key := channelhandler.KeyFromChannelID(id)
	now := time.Unix(2000, 0)
	runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{managerRetentionView(key, 9, 20, 20, 20, now)}}

	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas:       managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}},
		runtime:     runtime,
		stores:      &fakeManagerRetentionStores{cursor: 20, cursorExists: true},
		metadata:    &fakeManagerRetentionMetadata{},
		now:         func() time.Time { return now },
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  9,
	})

	require.NoError(t, err)
	require.Equal(t, managementusecase.MessageRetentionStatusNoop, got.Status)
	require.Equal(t, uint64(9), got.AdvancedThroughSeq)
	require.Equal(t, uint64(10), got.MinAvailableSeq)
}

func TestManagerMessageRetentionRequiresLeader(t *testing.T) {
	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas:       managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2}},
	}

	_, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		ThroughSeq:  9,
	})

	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
}

func TestManagerMessageRetentionForwardsRemoteLeader(t *testing.T) {
	id := channel.ChannelID{ID: "retain-remote", Type: 2}
	remote := &fakeManagerRetentionRemote{result: accessnode.ChannelRetentionAdvanceResult{
		ChannelID:           id,
		RequestedThroughSeq: 10,
		AdvancedThroughSeq:  8,
		MinAvailableSeq:     9,
		Status:              string(managementusecase.MessageRetentionStatusAdvanced),
	}}
	op := managerMessageRetentionOperator{
		localNodeID: 1,
		metas: managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Leader:      9,
		}},
		remote: remote,
	}

	got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
		ThroughSeq:  10,
	})

	require.NoError(t, err)
	require.Equal(t, uint64(9), remote.nodeID)
	require.Equal(t, accessnode.ChannelRetentionAdvanceRequest{ChannelID: id, ThroughSeq: 10}, remote.lastReq)
	require.Equal(t, managementusecase.MessageRetentionStatusAdvanced, got.Status)
	require.Equal(t, uint64(8), got.AdvancedThroughSeq)
}

func managerRetentionView(key channel.ChannelKey, current, hw, checkpointHW, minISR uint64, now time.Time) channel.RetentionView {
	return channel.RetentionView{
		ChannelKey:          key,
		Epoch:               7,
		LeaderEpoch:         8,
		Leader:              1,
		LeaseUntil:          now.Add(time.Minute),
		HW:                  hw,
		CheckpointHW:        checkpointHW,
		CommitReady:         true,
		RetentionThroughSeq: current,
		MinAvailableSeq:     channel.EffectiveMinAvailableSeq(current, 0),
		MinISRMatchOffset:   minISR,
	}
}

type fakeManagerRetentionRuntime struct {
	views   []channel.RetentionView
	applied []uint64
}

func (f *fakeManagerRetentionRuntime) RetentionView(channel.ChannelKey) (channel.RetentionView, error) {
	if len(f.views) == 0 {
		return channel.RetentionView{}, nil
	}
	view := f.views[0]
	if len(f.views) > 1 {
		f.views = f.views[1:]
	}
	return view, nil
}

func (f *fakeManagerRetentionRuntime) ApplyRetentionBoundary(_ context.Context, _ channel.ChannelKey, throughSeq uint64) error {
	f.applied = append(f.applied, throughSeq)
	return nil
}

type fakeManagerRetentionStores struct {
	cursor       uint64
	cursorExists bool
	loadCalls    int
	confirmCalls int
}

func (f *fakeManagerRetentionStores) LoadCommittedDispatchCursor(context.Context, channel.ChannelID, string) (uint64, bool, error) {
	f.loadCalls++
	return f.cursor, f.cursorExists, nil
}

func (f *fakeManagerRetentionStores) ConfirmCommittedDispatchCursorDurable(context.Context, channel.ChannelID, string, uint64) (uint64, error) {
	f.confirmCalls++
	return f.cursor, nil
}

type fakeManagerRetentionMetadata struct {
	calls   int
	lastReq metadb.ChannelRetentionAdvance
}

func (f *fakeManagerRetentionMetadata) AdvanceChannelRetentionThroughSeq(_ context.Context, req metadb.ChannelRetentionAdvance) error {
	f.calls++
	f.lastReq = req
	return nil
}

type fakeManagerRetentionRemote struct {
	nodeID  uint64
	lastReq accessnode.ChannelRetentionAdvanceRequest
	result  accessnode.ChannelRetentionAdvanceResult
	err     error
}

func (f *fakeManagerRetentionRemote) AdvanceChannelRetention(_ context.Context, nodeID uint64, req accessnode.ChannelRetentionAdvanceRequest) (accessnode.ChannelRetentionAdvanceResult, error) {
	f.nodeID = nodeID
	f.lastReq = req
	return f.result, f.err
}
