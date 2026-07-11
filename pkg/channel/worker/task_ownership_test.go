package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestTemporaryStoreTasksCloseEveryAcquiredHandle(t *testing.T) {
	wantErr := errors.New("store dependency failed")
	for _, tc := range temporaryStoreOwnershipCases() {
		t.Run(tc.name, func(t *testing.T) {
			modes := []struct {
				name   string
				ctx    context.Context
				failAt string
				want   error
			}{
				{name: "success", ctx: context.Background()},
				{name: "dependency_error", ctx: context.Background(), failAt: tc.operation, want: wantErr},
				{name: "canceled", ctx: canceledOwnershipContext(), want: context.Canceled},
			}
			for _, mode := range modes {
				t.Run(mode.name, func(t *testing.T) {
					factory := &trackingStoreFactory{failAt: mode.failAt, operationErr: wantErr}
					res := tc.task.Run(mode.ctx, Deps{Stores: factory})
					if mode.want == nil {
						require.NoError(t, res.Err)
					} else {
						require.ErrorIs(t, res.Err, mode.want)
					}
					handle := factory.requireLastHandle(t)
					require.Equal(t, int32(1), handle.closeCalls.Load())
				})
			}
		})
	}
}

func TestTemporaryStoreTasksCloseAcquisitionsWithoutOverwritingSuccess(t *testing.T) {
	closeErr := errors.New("lease close failed")
	for _, tc := range temporaryStoreOwnershipCases() {
		t.Run(tc.name, func(t *testing.T) {
			factory := &trackingStoreFactory{closeErr: closeErr}
			res := tc.task.Run(context.Background(), Deps{Stores: factory})
			require.NoError(t, res.Err)
			handle := factory.requireLastHandle(t)
			require.Equal(t, int32(1), handle.closeCalls.Load())
		})
	}
}

func TestStoreLookupClosesHandleWhenLookupCapabilityIsMissing(t *testing.T) {
	tc := temporaryStoreOwnershipCases()[3]
	factory := &trackingStoreFactory{withoutLookup: true}
	res := tc.task.Run(context.Background(), Deps{Stores: factory})
	require.ErrorIs(t, res.Err, ch.ErrInvalidConfig)
	require.Equal(t, int32(1), factory.requireLastHandle(t).closeCalls.Load())
}

func TestStoreLoadTransfersHandleOnlyAfterCompleteResult(t *testing.T) {
	task := ownershipStoreLoadTask()
	factory := &trackingStoreFactory{}
	res := task.Run(context.Background(), Deps{Stores: factory})
	require.NoError(t, res.Err)
	require.NotNil(t, res.StoreLoad)
	handle := factory.requireLastHandle(t)
	require.Zero(t, handle.closeCalls.Load())
	require.Same(t, factory.lastReturned, res.StoreLoad.Store)
	require.NoError(t, res.StoreLoad.Store.Close())
	require.Equal(t, int32(1), handle.closeCalls.Load())
}

func TestStoreLoadClosesHandleOnLoadAndRetentionErrors(t *testing.T) {
	wantErr := errors.New("load failed")
	for _, failAt := range []string{"load", "load-retention"} {
		t.Run(failAt, func(t *testing.T) {
			factory := &trackingStoreFactory{failAt: failAt, operationErr: wantErr}
			res := ownershipStoreLoadTask().Run(context.Background(), Deps{Stores: factory})
			require.ErrorIs(t, res.Err, wantErr)
			require.Nil(t, res.StoreLoad)
			require.Equal(t, int32(1), factory.requireLastHandle(t).closeCalls.Load())
		})
	}
}

func TestStoreLoadClosesHandleOnPanic(t *testing.T) {
	for _, panicAt := range []string{"load", "load-retention"} {
		t.Run(panicAt, func(t *testing.T) {
			factory := &trackingStoreFactory{panicAt: panicAt}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_ = ownershipStoreLoadTask().Run(context.Background(), Deps{Stores: factory})
			}()
			require.NotNil(t, recovered)
			require.Equal(t, int32(1), factory.requireLastHandle(t).closeCalls.Load())
		})
	}
}

func TestStoreLoadNilHandleRemainsInvalid(t *testing.T) {
	factory := &trackingStoreFactory{returnNil: true}
	res := ownershipStoreLoadTask().Run(context.Background(), Deps{Stores: factory})
	require.ErrorIs(t, res.Err, ch.ErrInvalidConfig)
	require.Nil(t, res.StoreLoad)
}

type temporaryStoreOwnershipCase struct {
	name      string
	operation string
	task      Task
}

func temporaryStoreOwnershipCases() []temporaryStoreOwnershipCase {
	id := ch.ChannelID{ID: "ownership", Type: 1}
	fence := ch.Fence{ChannelKey: "ownership:1", OpID: 1}
	return []temporaryStoreOwnershipCase{
		{
			name:      "retention",
			operation: "retention-adopt",
			task: Task{Kind: TaskStoreRetention, Fence: fence, StoreRetention: &StoreRetentionTask{
				ChannelID: id, ThroughSeq: 1, TrimAllowed: true,
			}},
		},
		{
			name:      "append",
			operation: "append",
			task: Task{Kind: TaskStoreAppend, Fence: fence, StoreAppend: &StoreAppendTask{
				ChannelID: id, Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
			}},
		},
		{
			name:      "read_log",
			operation: "read-log",
			task: Task{Kind: TaskStoreReadLog, Fence: fence, StoreReadLog: &StoreReadLogTask{
				ChannelID: id, FromOffset: 1, MaxOffset: 1, MaxBytes: 1024,
			}},
		},
		{
			name:      "lookup_message",
			operation: "lookup",
			task: Task{Kind: TaskStoreLookupMessage, Fence: fence, StoreLookupMessage: &StoreLookupMessageTask{
				ChannelID: id, MessageID: 1,
			}},
		},
		{
			name:      "apply",
			operation: "apply",
			task: Task{Kind: TaskStoreApply, Fence: fence, StoreApply: &StoreApplyTask{
				ChannelID: id, Records: []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}}, LeaderHW: 1,
			}},
		},
		{
			name:      "checkpoint",
			operation: "checkpoint",
			task: Task{Kind: TaskStoreCheckpoint, Fence: fence, StoreCheckpoint: &StoreCheckpointTask{
				ChannelID: id, Checkpoint: ch.Checkpoint{HW: 1},
			}},
		},
	}
}

func ownershipStoreLoadTask() Task {
	id := ch.ChannelID{ID: "load-ownership", Type: 1}
	return Task{
		Kind:      TaskStoreLoad,
		Fence:     ch.Fence{ChannelKey: "load-ownership:1", OpID: 2},
		StoreLoad: &StoreLoadTask{ChannelID: id},
	}
}

func canceledOwnershipContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

type trackingStoreFactory struct {
	mu            sync.Mutex
	failAt        string
	panicAt       string
	operationErr  error
	closeErr      error
	returnNil     bool
	withoutLookup bool
	handles       []*trackingStoreHandle
	lastReturned  store.ChannelStore
}

func (f *trackingStoreFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	if f.returnNil {
		return nil, nil
	}
	handle := &trackingStoreHandle{
		failAt:       f.failAt,
		panicAt:      f.panicAt,
		operationErr: f.operationErr,
		closeErr:     f.closeErr,
	}
	var returned store.ChannelStore = handle
	if f.withoutLookup {
		returned = &trackingStoreWithoutLookup{ChannelStore: handle}
	}
	f.mu.Lock()
	f.handles = append(f.handles, handle)
	f.lastReturned = returned
	f.mu.Unlock()
	return returned, nil
}

func (f *trackingStoreFactory) requireLastHandle(t *testing.T) *trackingStoreHandle {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.handles)
	return f.handles[len(f.handles)-1]
}

type trackingStoreWithoutLookup struct {
	store.ChannelStore
}

type trackingStoreHandle struct {
	failAt       string
	panicAt      string
	operationErr error
	closeErr     error
	closeCalls   atomic.Int32
}

func (h *trackingStoreHandle) before(ctx context.Context, operation string) error {
	if h.panicAt == operation {
		panic("tracking store panic: " + operation)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.failAt == operation {
		return h.operationErr
	}
	return nil
}

func (h *trackingStoreHandle) Load(ctx context.Context) (store.InitialState, error) {
	if err := h.before(ctx, "load"); err != nil {
		return store.InitialState{}, err
	}
	return store.InitialState{LEO: 1, HW: 1, CheckpointHW: 1}, nil
}

func (h *trackingStoreHandle) AppendLeader(ctx context.Context, _ store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	if err := h.before(ctx, "append"); err != nil {
		return store.AppendLeaderResult{}, err
	}
	return store.AppendLeaderResult{BaseOffset: 1, LastOffset: 1}, nil
}

func (h *trackingStoreHandle) ApplyFollower(ctx context.Context, _ store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	if err := h.before(ctx, "apply"); err != nil {
		return store.ApplyFollowerResult{}, err
	}
	return store.ApplyFollowerResult{LEO: 1}, nil
}

func (h *trackingStoreHandle) ReadCommitted(context.Context, store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return store.ReadCommittedResult{}, nil
}

func (h *trackingStoreHandle) ReadLog(ctx context.Context, _ store.ReadLogRequest) (store.ReadLogResult, error) {
	if err := h.before(ctx, "read-log"); err != nil {
		return store.ReadLogResult{}, err
	}
	return store.ReadLogResult{Records: []ch.Record{{ID: 1, Index: 1}}}, nil
}

func (h *trackingStoreHandle) LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error) {
	if err := h.before(ctx, "lookup"); err != nil {
		return ch.Message{}, false, err
	}
	return ch.Message{MessageID: messageID, MessageSeq: 1}, true, nil
}

func (h *trackingStoreHandle) LoadRetentionState(ctx context.Context) (store.RetentionState, error) {
	if err := h.before(ctx, "load-retention"); err != nil {
		return store.RetentionState{}, err
	}
	return store.RetentionState{LocalRetentionThroughSeq: 1, PhysicalRetentionThroughSeq: 1, RetainedMaxSeq: 1}, nil
}

func (h *trackingStoreHandle) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, _ string) (uint64, error) {
	if err := h.before(ctx, "retention-adopt"); err != nil {
		return 0, err
	}
	return throughSeq, nil
}

func (h *trackingStoreHandle) TrimMessagesThrough(ctx context.Context, throughSeq uint64, _ store.RetentionTrimOptions) (store.RetentionTrimResult, error) {
	if err := h.before(ctx, "retention-trim"); err != nil {
		return store.RetentionTrimResult{}, err
	}
	return store.RetentionTrimResult{DeletedThroughSeq: throughSeq, Deleted: 1}, nil
}

func (h *trackingStoreHandle) StoreCheckpoint(ctx context.Context, _ ch.Checkpoint) error {
	return h.before(ctx, "checkpoint")
}

func (h *trackingStoreHandle) Close() error {
	h.closeCalls.Add(1)
	return h.closeErr
}
