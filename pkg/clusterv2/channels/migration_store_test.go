package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

func TestMigrationStoreCreateLeaderTransferUsesGuardedCommandAndRoutesSlot(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000000000).UTC()
	id := ch.ChannelID{ID: "migration-create-leader", Type: 1}
	meta := testMigrationRuntimeMeta(id)
	reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{7: meta}}
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 7, SlotID: 11, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
		Now:       func() time.Time { return now },
	})

	task, err := store.CreateLeaderTransfer(ctx, CreateLeaderTransferRequest{
		ChannelID:     id,
		TaskID:        "task-leader-transfer",
		DesiredLeader: 3,
	})

	require.NoError(t, err)
	require.Equal(t, uint32(11), proposer.lastSlotID)
	require.Equal(t, uint16(7), proposer.lastHashSlot)
	require.Equal(t, []uint16{7}, reader.runtimeMetaHashSlots)
	require.Equal(t, metadb.ChannelMigrationKindLeaderTransfer, task.Kind)
	require.Equal(t, uint64(1), task.SourceNode)
	require.Equal(t, uint64(3), task.TargetNode)
	require.Equal(t, meta.ChannelEpoch, task.BaseChannelEpoch)
	require.Equal(t, meta.LeaderEpoch, task.BaseLeaderEpoch)
	require.Equal(t, now.UnixMilli(), task.CreatedAtMS)
	require.Equal(t, metafsm.EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: migrationRuntimeGuard(meta),
	}), proposer.lastCommand)
}

func TestMigrationStoreCreateLeaderTransferValidatesDesiredLeader(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-leader-validation", Type: 1}
	baseMeta := testMigrationRuntimeMeta(id)

	tests := []struct {
		name          string
		meta          metadb.ChannelRuntimeMeta
		desiredLeader ch.NodeID
	}{
		{name: "current leader", meta: baseMeta, desiredLeader: ch.NodeID(baseMeta.Leader)},
		{name: "not replica", meta: baseMeta, desiredLeader: 4},
		{name: "not isr", meta: func() metadb.ChannelRuntimeMeta {
			meta := baseMeta
			meta.ISR = []uint64{1, 2}
			return meta
		}(), desiredLeader: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proposer := &fakeMigrationProposer{}
			store := NewMigrationStore(MigrationStoreConfig{
				LocalNode: 2,
				Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 7, SlotID: 11, Leader: 2}}},
				Proposer:  proposer,
				Reader:    &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{7: tc.meta}},
			})

			_, err := store.CreateLeaderTransfer(ctx, CreateLeaderTransferRequest{
				ChannelID:     id,
				TaskID:        "task-invalid-leader",
				DesiredLeader: tc.desiredLeader,
			})

			require.ErrorIs(t, err, ch.ErrInvalidConfig)
			require.Empty(t, proposer.lastCommand)
		})
	}
}

func TestMigrationStoreCreateReplicaReplaceReturnsDuplicateActiveTask(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-create-duplicate", Type: 1}
	meta := testMigrationRuntimeMeta(id)
	reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{8: meta}}
	proposer := &fakeMigrationProposer{err: metadb.ErrAlreadyExists}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 8, SlotID: 12, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
		Now:       func() time.Time { return time.UnixMilli(1750000000000).UTC() },
	})

	_, err := store.CreateReplicaReplace(ctx, CreateReplicaReplaceRequest{
		ChannelID:  id,
		TaskID:     "task-duplicate",
		SourceNode: 3,
		TargetNode: 4,
	})

	require.ErrorIs(t, err, metadb.ErrAlreadyExists)
}

func TestMigrationStoreCreateReplicaReplaceValidatesReplicaMembership(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-replace-validation", Type: 1}
	meta := testMigrationRuntimeMeta(id)

	tests := []struct {
		name   string
		source ch.NodeID
		target ch.NodeID
	}{
		{name: "source not replica", source: 4, target: 5},
		{name: "target already replica", source: 2, target: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proposer := &fakeMigrationProposer{}
			store := NewMigrationStore(MigrationStoreConfig{
				LocalNode: 2,
				Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 8, SlotID: 12, Leader: 2}}},
				Proposer:  proposer,
				Reader:    &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{8: meta}},
			})

			_, err := store.CreateReplicaReplace(ctx, CreateReplicaReplaceRequest{
				ChannelID:  id,
				TaskID:     "task-invalid-replace",
				SourceNode: tc.source,
				TargetNode: tc.target,
			})

			require.ErrorIs(t, err, ch.ErrInvalidConfig)
			require.Empty(t, proposer.lastCommand)
		})
	}
}

func TestMigrationStoreClaimWritesOwnerNodeAndLeaseEpoch(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000002000).UTC()
	id := ch.ChannelID{ID: "migration-claim", Type: 1}
	task := testMigrationTask(id, "task-claim")
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 9,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 9, SlotID: 13, Leader: 9}}},
		Proposer:  proposer,
		Reader:    &fakeMigrationReader{},
		Now:       func() time.Time { return now },
		OwnerTTL:  45 * time.Second,
	})

	err := store.Claim(ctx, task, task.UpdatedAtMS)

	require.NoError(t, err)
	want := metadb.ChannelMigrationTaskClaim{
		Guard:             migrationTaskGuard(task, task.UpdatedAtMS),
		Status:            metadb.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       9,
		OwnerLeaseUntilMS: now.Add(45 * time.Second).UnixMilli(),
		NowMS:             now.UnixMilli(),
		UpdatedAtMS:       now.UnixMilli(),
	}
	require.Equal(t, uint32(13), proposer.lastSlotID)
	require.Equal(t, uint16(9), proposer.lastHashSlot)
	require.Equal(t, metafsm.EncodeClaimChannelMigrationTaskCommand(want), proposer.lastCommand)
}

func TestMigrationStoreClaimRejectsMissingLocalOwner(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-claim-no-owner", Type: 1}
	task := testMigrationTask(id, "task-claim-no-owner")
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		Router:   fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 9, SlotID: 13, Leader: 9}}},
		Proposer: proposer,
		Reader:   &fakeMigrationReader{},
	})

	err := store.Claim(ctx, task, task.UpdatedAtMS)

	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Empty(t, proposer.lastCommand)
}

func TestMigrationStoreAdvanceUsesExpectedTaskVersion(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000003000).UTC()
	id := ch.ChannelID{ID: "migration-advance", Type: 1}
	task := testMigrationTask(id, "task-advance")
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 10, SlotID: 14, Leader: 2}}},
		Proposer:  proposer,
		Reader:    &fakeMigrationReader{},
		Now:       func() time.Time { return now },
	})

	err := store.Advance(ctx, task, 12345, metadb.ChannelMigrationPhaseProbeTarget, metadb.ChannelMigrationStatusBlocked, "target lagging")

	require.NoError(t, err)
	want := metadb.ChannelMigrationTaskAdvance{
		Guard:          migrationTaskGuard(task, 12345),
		Status:         metadb.ChannelMigrationStatusBlocked,
		Phase:          metadb.ChannelMigrationPhaseProbeTarget,
		Attempt:        task.Attempt + 1,
		BlockerMessage: "target lagging",
		UpdatedAtMS:    now.UnixMilli(),
	}
	require.Equal(t, metafsm.EncodeAdvanceChannelMigrationTaskCommand(want), proposer.lastCommand)
}

func TestMigrationStoreSetAndClearWriteFenceUseTaskToken(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000004000).UTC()
	id := ch.ChannelID{ID: "migration-fence", Type: 1}
	task := testMigrationTask(id, "task-fence")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.UpdatedAtMS = 1750000001000
	meta := testMigrationRuntimeMeta(id)
	meta.WriteFenceVersion = 6
	reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{12: meta}}
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 12, SlotID: 16, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
		Now:       func() time.Time { return now },
		FenceTTL:  30 * time.Second,
	})

	err := store.SetWriteFence(ctx, task, ch.WriteFenceReasonReplicaReplace)

	require.NoError(t, err)
	setReq := metadb.ChannelMigrationFenceRequest{
		Guard:        migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard: migrationRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseCutoverFence,
		FenceReason:  uint8(ch.WriteFenceReasonReplicaReplace),
		FenceUntilMS: now.Add(30 * time.Second).UnixMilli(),
		UpdatedAtMS:  now.UnixMilli(),
	}
	require.Equal(t, metafsm.EncodeSetChannelWriteFenceCommand(setReq), proposer.lastCommand)

	fencedTask := task
	fencedTask.FenceToken = task.TaskID
	fencedTask.FenceVersion = meta.WriteFenceVersion + 1
	fencedTask.FenceUntilMS = setReq.FenceUntilMS
	fencedTask.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	fencedTask.UpdatedAtMS = now.UnixMilli()
	fencedMeta := meta
	fencedMeta.WriteFenceToken = task.TaskID
	fencedMeta.WriteFenceVersion++
	fencedMeta.WriteFenceReason = uint8(ch.WriteFenceReasonReplicaReplace)
	fencedMeta.WriteFenceUntilMS = setReq.FenceUntilMS
	reader.runtimeMeta[12] = fencedMeta

	err = store.ClearWriteFence(ctx, fencedTask)

	require.NoError(t, err)
	clearUpdatedAtMS := fencedTask.UpdatedAtMS + 1
	clearReq := metadb.ChannelMigrationClearFenceRequest{
		Guard:         migrationTaskGuard(fencedTask, fencedTask.UpdatedAtMS),
		RuntimeGuard:  migrationRuntimeGuard(fencedMeta),
		Status:        metadb.ChannelMigrationStatusCompleted,
		Phase:         metadb.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   clearUpdatedAtMS,
		CompletedAtMS: clearUpdatedAtMS,
	}
	require.Equal(t, metafsm.EncodeClearChannelWriteFenceCommand(clearReq), proposer.lastCommand)
	require.Equal(t, "task-fence", clearReq.RuntimeGuard.ExpectedFenceToken)
	require.Equal(t, fencedTask.FenceVersion, clearReq.RuntimeGuard.ExpectedFenceVersion)
}

func TestMigrationStoreClearWriteFenceResumesAfterEmbeddedLeaderTransfer(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000006000).UTC()
	id := ch.ChannelID{ID: "migration-embedded-clear", Type: 1}
	task := testMigrationTask(id, "task-embedded-clear")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
	task.EmbeddedLeaderTransfer = true
	task.EmbeddedDesiredLeader = 2
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000010000
	task.UpdatedAtMS = 1750000005000
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 2
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = task.FenceVersion
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonLeaderTransfer)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{13: meta}}
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 13, SlotID: 17, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
		Now:       func() time.Time { return now },
	})

	err := store.ClearWriteFence(ctx, task)

	require.NoError(t, err)
	want := metadb.ChannelMigrationClearFenceRequest{
		Guard:        migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard: migrationRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseAddLearner,
		UpdatedAtMS:  now.UnixMilli(),
	}
	require.Equal(t, metafsm.EncodeClearChannelWriteFenceCommand(want), proposer.lastCommand)
}

func TestMigrationStoreReadsAreScopedToChannelHashSlot(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-read-scoped", Type: 1}
	active := testMigrationTask(id, "task-active")
	reader := &fakeMigrationReader{
		active: map[uint16]metadb.ChannelMigrationTask{21: active},
		tasks:  map[uint16]metadb.ChannelMigrationTask{21: active},
	}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 21, SlotID: 31, Leader: 2}}},
		Proposer:  &fakeMigrationProposer{},
		Reader:    reader,
	})

	gotActive, ok, err := store.GetActive(ctx, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, active.TaskID, gotActive.TaskID)

	gotByID, ok, err := store.Get(ctx, id, active.TaskID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, active.TaskID, gotByID.TaskID)

	list, err := store.ListActive(ctx, id, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, []uint16{21}, reader.activeHashSlots)
	require.Equal(t, []uint16{21}, reader.taskHashSlots)
	require.Equal(t, []uint16{21}, reader.listHashSlots)
}

func TestMigrationStoreAbortUsesRuntimeGuardAndReason(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000005000).UTC()
	id := ch.ChannelID{ID: "migration-abort", Type: 1}
	task := testMigrationTask(id, "task-abort")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.UpdatedAtMS = 1750000001000
	meta := testMigrationRuntimeMeta(id)
	reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{22: meta}}
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 22, SlotID: 32, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
		Now:       func() time.Time { return now },
	})

	err := store.Abort(ctx, task, "operator aborted")

	require.NoError(t, err)
	want := metadb.ChannelMigrationAbortRequest{
		Guard:         migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard:  migrationRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusAborted,
		Phase:         task.Phase,
		UpdatedAtMS:   now.UnixMilli(),
		CompletedAtMS: now.UnixMilli(),
		LastError:     "operator aborted",
	}
	require.Equal(t, metafsm.EncodeAbortChannelMigrationCommand(want), proposer.lastCommand)
}

func TestMigrationStoreRejectsInvalidTaskChannelType(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-invalid-task-type", Type: 1}
	task := testMigrationTask(id, "task-invalid-type")
	task.ChannelType = 256
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 23, SlotID: 33, Leader: 2}}},
		Proposer:  proposer,
		Reader:    &fakeMigrationReader{},
	})

	err := store.Claim(ctx, task, task.UpdatedAtMS)

	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Empty(t, proposer.lastCommand)
}

func TestMigrationStoreRejectsRuntimeMetaIdentityMismatch(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "migration-meta-mismatch", Type: 1}
	meta := testMigrationRuntimeMeta(ch.ChannelID{ID: "different-channel", Type: 1})
	reader := &fakeMigrationReader{
		runtimeMeta:               map[uint16]metadb.ChannelRuntimeMeta{24: meta},
		runtimeMetaBypassIdentity: true,
	}
	proposer := &fakeMigrationProposer{}
	store := NewMigrationStore(MigrationStoreConfig{
		LocalNode: 2,
		Router:    fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 24, SlotID: 34, Leader: 2}}},
		Proposer:  proposer,
		Reader:    reader,
	})

	_, err := store.CreateLeaderTransfer(ctx, CreateLeaderTransferRequest{
		ChannelID:     id,
		TaskID:        "task-meta-mismatch",
		DesiredLeader: 3,
	})

	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Empty(t, proposer.lastCommand)
}

func TestMigrationStoreServiceExposesConfiguredStore(t *testing.T) {
	migration := NewMigrationStore(MigrationStoreConfig{})

	svc, err := NewService(Config{Runtime: &fakeRuntime{}, MigrationStore: migration})

	require.NoError(t, err)
	require.Same(t, migration, svc.MigrationStore())
}

func testMigrationRuntimeMeta(id ch.ChannelID) metadb.ChannelRuntimeMeta {
	return metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID:       id.ID,
		ChannelType:     int64(id.Type),
		ChannelEpoch:    10,
		LeaderEpoch:     20,
		RouteGeneration: 30,
		Replicas:        []uint64{1, 2, 3},
		ISR:             []uint64{1, 2, 3},
		Leader:          1,
		MinISR:          2,
		Status:          uint8(ch.StatusActive),
		LeaseUntilMS:    1750001000000,
	})
}

func testMigrationTask(id ch.ChannelID, taskID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       1,
		TargetNode:       4,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

type fakeMigrationRouter struct {
	routes map[string]routing.Route
	err    error
}

func (r fakeMigrationRouter) RouteKey(key string) (routing.Route, error) {
	if r.err != nil {
		return routing.Route{}, r.err
	}
	route, ok := r.routes[key]
	if !ok {
		return routing.Route{}, routing.ErrRouteNotReady
	}
	return route, nil
}

type fakeMigrationProposer struct {
	lastSlotID   uint32
	lastHashSlot uint16
	lastCommand  []byte
	err          error
}

func (p *fakeMigrationProposer) ProposeChannelMigrationCommand(_ context.Context, slotID uint32, hashSlot uint16, command []byte) error {
	p.lastSlotID = slotID
	p.lastHashSlot = hashSlot
	p.lastCommand = append([]byte(nil), command...)
	return p.err
}

type fakeMigrationReader struct {
	runtimeMeta map[uint16]metadb.ChannelRuntimeMeta
	active      map[uint16]metadb.ChannelMigrationTask
	tasks       map[uint16]metadb.ChannelMigrationTask

	runtimeMetaBypassIdentity bool

	runtimeMetaHashSlots []uint16
	activeHashSlots      []uint16
	taskHashSlots        []uint16
	listHashSlots        []uint16
}

func (r *fakeMigrationReader) GetChannelRuntimeMeta(_ context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	r.runtimeMetaHashSlots = append(r.runtimeMetaHashSlots, hashSlot)
	meta, ok := r.runtimeMeta[hashSlot]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	if r.runtimeMetaBypassIdentity {
		return meta, nil
	}
	if meta.ChannelID != channelID || meta.ChannelType != channelType {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func (r *fakeMigrationReader) GetActiveChannelMigrationTask(_ context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	r.activeHashSlots = append(r.activeHashSlots, hashSlot)
	task, ok := r.active[hashSlot]
	if !ok || task.ChannelID != channelID || task.ChannelType != channelType {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	return task, true, nil
}

func (r *fakeMigrationReader) GetChannelMigrationTask(_ context.Context, hashSlot uint16, channelID string, channelType int64, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	r.taskHashSlots = append(r.taskHashSlots, hashSlot)
	task, ok := r.tasks[hashSlot]
	if !ok || task.ChannelID != channelID || task.ChannelType != channelType || task.TaskID != taskID {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	return task, true, nil
}

func (r *fakeMigrationReader) ListActiveChannelMigrationTasks(_ context.Context, hashSlot uint16, limit int) ([]metadb.ChannelMigrationTask, error) {
	r.listHashSlots = append(r.listHashSlots, hashSlot)
	task, ok := r.active[hashSlot]
	if !ok {
		return nil, nil
	}
	if limit == 0 {
		return nil, nil
	}
	return []metadb.ChannelMigrationTask{task}, nil
}

var _ MigrationCommandProposer = (*fakeMigrationProposer)(nil)
var _ MigrationTaskReader = (*fakeMigrationReader)(nil)
