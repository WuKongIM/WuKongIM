package management

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelMigrationLeaderTransferRejectsTargetOutsideReplicas(t *testing.T) {
	store := &fakeChannelMigrationStore{err: ch.ErrInvalidConfig}
	app := New(Options{ChannelMigration: store})

	_, err := app.RequestChannelLeaderTransfer(context.Background(), LeaderTransferInput{
		ChannelID:   "g1",
		ChannelType: 1,
		TargetNode:  9,
	})

	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("RequestChannelLeaderTransfer() error = %v, want invalid argument", err)
	}
}

func TestChannelMigrationReplicaReplacementRejectsTargetAlreadyReplica(t *testing.T) {
	store := &fakeChannelMigrationStore{err: ch.ErrInvalidConfig}
	app := New(Options{ChannelMigration: store})

	_, err := app.RequestChannelReplicaReplace(context.Background(), ReplicaReplaceInput{
		ChannelID:   "g1",
		ChannelType: 1,
		SourceNode:  3,
		TargetNode:  2,
	})

	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("RequestChannelReplicaReplace() error = %v, want invalid argument", err)
	}
}

func TestChannelMigrationDuplicateActiveTaskMapsToConflict(t *testing.T) {
	store := &fakeChannelMigrationStore{err: metadb.ErrAlreadyExists}
	app := New(Options{ChannelMigration: store})

	_, err := app.RequestChannelLeaderTransfer(context.Background(), LeaderTransferInput{
		ChannelID:   "g1",
		ChannelType: 1,
		TargetNode:  2,
	})

	if !errors.Is(err, ErrChannelMigrationConflict) {
		t.Fatalf("RequestChannelLeaderTransfer() error = %v, want conflict", err)
	}
}

func TestChannelMigrationSuccessfulRequestReturnsTaskSummary(t *testing.T) {
	task := metadb.ChannelMigrationTask{
		TaskID:        "task-g1",
		Kind:          metadb.ChannelMigrationKindLeaderTransfer,
		Status:        metadb.ChannelMigrationStatusPending,
		Phase:         metadb.ChannelMigrationPhaseValidate,
		ChannelID:     "g1",
		ChannelType:   1,
		SourceNode:    1,
		TargetNode:    2,
		DesiredLeader: 2,
	}
	store := &fakeChannelMigrationStore{task: task}
	app := New(Options{ChannelMigration: store})

	got, err := app.RequestChannelLeaderTransfer(context.Background(), LeaderTransferInput{
		ChannelID:   "g1",
		ChannelType: 1,
		TargetNode:  2,
	})

	if err != nil {
		t.Fatalf("RequestChannelLeaderTransfer() error = %v", err)
	}
	if !store.leaderTransferCalled {
		t.Fatalf("CreateLeaderTransfer was not called")
	}
	if got.TaskID != "task-g1" ||
		got.ChannelID != "g1" ||
		got.ChannelType != 1 ||
		got.SourceNode != 1 ||
		got.TargetNode != 2 ||
		got.Kind != "leader_transfer" ||
		got.Status != "pending" ||
		got.Phase != "validate" {
		t.Fatalf("summary = %#v, want stable task summary", got)
	}
	if store.leaderTransferReq.ChannelID.ID != "g1" || store.leaderTransferReq.ChannelID.Type != 1 || uint64(store.leaderTransferReq.DesiredLeader) != 2 {
		t.Fatalf("leader transfer request = %#v, want channel g1 type 1 target 2", store.leaderTransferReq)
	}
}

func TestChannelMigrationListActiveClampsLimit(t *testing.T) {
	store := &fakeChannelMigrationStore{task: metadb.ChannelMigrationTask{TaskID: "task-g1"}}
	app := New(Options{ChannelMigration: store})

	_, err := app.ListActiveChannelMigrations(context.Background(), ChannelMigrationListInput{
		ChannelID:   "g1",
		ChannelType: 1,
		Limit:       500,
	})

	if err != nil {
		t.Fatalf("ListActiveChannelMigrations() error = %v", err)
	}
	if store.lastListLimit != 100 {
		t.Fatalf("ListActive limit = %d, want 100", store.lastListLimit)
	}
}

func TestChannelMigrationListActiveLabelsLeaderFailover(t *testing.T) {
	store := &fakeChannelMigrationStore{task: metadb.ChannelMigrationTask{
		TaskID:      "task-failover-g1",
		Kind:        metadb.ChannelMigrationKindLeaderFailover,
		Status:      metadb.ChannelMigrationStatusRunning,
		Phase:       metadb.ChannelMigrationPhaseDrainLeader,
		ChannelID:   "g1",
		ChannelType: 1,
		SourceNode:  1,
		TargetNode:  3,
	}}
	app := New(Options{ChannelMigration: store})

	got, err := app.ListActiveChannelMigrations(context.Background(), ChannelMigrationListInput{
		ChannelID:   "g1",
		ChannelType: 1,
		Limit:       20,
	})

	if err != nil {
		t.Fatalf("ListActiveChannelMigrations() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Kind != "leader_failover" {
		t.Fatalf("items = %#v, want leader_failover summary", got.Items)
	}
}

func TestChannelMigrationReadAndAbortPreserveExactChannelScope(t *testing.T) {
	task := metadb.ChannelMigrationTask{
		TaskID:        "task-g1",
		Kind:          metadb.ChannelMigrationKindReplicaReplace,
		Status:        metadb.ChannelMigrationStatusRunning,
		Phase:         metadb.ChannelMigrationPhaseWarmCatchUp,
		ChannelID:     "group-1",
		ChannelType:   2,
		SourceNode:    3,
		TargetNode:    4,
		DesiredLeader: 2,
	}
	store := &fakeChannelMigrationStore{task: task}
	app := New(Options{ChannelMigration: store})

	active, ok, err := app.ActiveChannelMigration(context.Background(), ChannelMigrationListInput{
		ChannelID: "  group-1  ", ChannelType: 2,
	})
	if err != nil || !ok {
		t.Fatalf("ActiveChannelMigration() = %#v, %v, %v", active, ok, err)
	}
	if store.lastActiveChannelID != (ch.ChannelID{ID: "group-1", Type: 2}) {
		t.Fatalf("active channel scope = %#v", store.lastActiveChannelID)
	}
	if active.TaskID != task.TaskID || active.Kind != "replica_replace" || active.Phase != "warm_catch_up" {
		t.Fatalf("active summary = %#v", active)
	}

	lookup, err := app.ChannelMigration(context.Background(), ChannelMigrationLookupInput{
		ChannelID: " group-1 ", ChannelType: 2, TaskID: " task-g1 ",
	})
	if err != nil {
		t.Fatalf("ChannelMigration() error = %v", err)
	}
	if store.lastGetChannelID != (ch.ChannelID{ID: "group-1", Type: 2}) || store.lastGetTaskID != "task-g1" {
		t.Fatalf("lookup scope = %#v/%q", store.lastGetChannelID, store.lastGetTaskID)
	}
	if lookup.TaskID != task.TaskID {
		t.Fatalf("lookup summary = %#v", lookup)
	}

	aborted, err := app.AbortChannelMigration(context.Background(), ChannelMigrationAbortInput{
		ChannelID: " group-1 ", ChannelType: 2, TaskID: " task-g1 ", Reason: " operator requested ",
	})
	if err != nil {
		t.Fatalf("AbortChannelMigration() error = %v", err)
	}
	if store.abortReason != "operator requested" || store.abortedTask.TaskID != task.TaskID {
		t.Fatalf("abort request = task %#v reason %q", store.abortedTask, store.abortReason)
	}
	if aborted.Status != "aborted" || aborted.LastError != "operator requested" {
		t.Fatalf("aborted summary = %#v", aborted)
	}
}

func TestChannelMigrationAbsenceAndUnavailableRemainDistinct(t *testing.T) {
	store := &fakeChannelMigrationStore{}
	app := New(Options{ChannelMigration: store})

	if got, ok, err := app.ActiveChannelMigration(context.Background(), ChannelMigrationListInput{ChannelID: "g1", ChannelType: 1}); err != nil || ok || got != (ChannelMigrationSummary{}) {
		t.Fatalf("absent active migration = %#v, %v, %v", got, ok, err)
	}
	if _, err := app.ChannelMigration(context.Background(), ChannelMigrationLookupInput{ChannelID: "g1", ChannelType: 1, TaskID: "missing"}); !errors.Is(err, ErrChannelMigrationNotFound) {
		t.Fatalf("missing lookup error = %v", err)
	}
	if _, err := app.AbortChannelMigration(context.Background(), ChannelMigrationAbortInput{ChannelID: "g1", ChannelType: 1, TaskID: "missing"}); !errors.Is(err, ErrChannelMigrationNotFound) {
		t.Fatalf("missing abort error = %v", err)
	}

	var unavailable *App
	if _, _, err := unavailable.ActiveChannelMigration(context.Background(), ChannelMigrationListInput{ChannelID: "g1", ChannelType: 1}); !errors.Is(err, ErrChannelMigrationUnavailable) {
		t.Fatalf("unwired active migration error = %v", err)
	}
	if _, err := unavailable.ChannelMigration(context.Background(), ChannelMigrationLookupInput{ChannelID: "g1", ChannelType: 1, TaskID: "task"}); !errors.Is(err, ErrChannelMigrationUnavailable) {
		t.Fatalf("unwired lookup error = %v", err)
	}
	if _, err := unavailable.AbortChannelMigration(context.Background(), ChannelMigrationAbortInput{ChannelID: "g1", ChannelType: 1, TaskID: "task"}); !errors.Is(err, ErrChannelMigrationUnavailable) {
		t.Fatalf("unwired abort error = %v", err)
	}
}

func TestChannelMigrationReplicaReplacePreservesIdempotencyAndNodeRoles(t *testing.T) {
	store := &fakeChannelMigrationStore{task: metadb.ChannelMigrationTask{
		TaskID: "replace-g1", Kind: metadb.ChannelMigrationKindReplicaReplace,
		Status: metadb.ChannelMigrationStatusPending, Phase: metadb.ChannelMigrationPhaseValidate,
		ChannelID: "g1", ChannelType: 1, SourceNode: 2, TargetNode: 4,
	}}
	app := New(Options{ChannelMigration: store})

	got, err := app.RequestChannelReplicaReplace(context.Background(), ReplicaReplaceInput{
		ChannelID: " g1 ", ChannelType: 1, SourceNode: 2, TargetNode: 4, TaskID: " replace-g1 ",
	})
	if err != nil {
		t.Fatalf("RequestChannelReplicaReplace() error = %v", err)
	}
	if !store.replicaReplaceCalled || store.replicaReplaceReq.ChannelID != (ch.ChannelID{ID: "g1", Type: 1}) ||
		store.replicaReplaceReq.TaskID != "replace-g1" || store.replicaReplaceReq.SourceNode != 2 || store.replicaReplaceReq.TargetNode != 4 {
		t.Fatalf("replica replace request = %#v", store.replicaReplaceReq)
	}
	if got.Kind != "replica_replace" || got.SourceNode != 2 || got.TargetNode != 4 {
		t.Fatalf("replica replace summary = %#v", got)
	}
}

func TestChannelMigrationSummaryPublishesStableLifecycleVocabulary(t *testing.T) {
	statuses := []struct {
		status metadb.ChannelMigrationStatus
		want   string
	}{
		{metadb.ChannelMigrationStatusPending, "pending"},
		{metadb.ChannelMigrationStatusRunning, "running"},
		{metadb.ChannelMigrationStatusBlocked, "blocked"},
		{metadb.ChannelMigrationStatusCompleted, "completed"},
		{metadb.ChannelMigrationStatusFailed, "failed"},
		{metadb.ChannelMigrationStatusAborted, "aborted"},
		{metadb.ChannelMigrationStatus(255), "unknown"},
	}
	for _, tc := range statuses {
		if got := managerChannelMigrationSummary(metadb.ChannelMigrationTask{Status: tc.status}).Status; got != tc.want {
			t.Fatalf("status %d = %q, want %q", tc.status, got, tc.want)
		}
	}

	phases := []struct {
		phase metadb.ChannelMigrationPhase
		want  string
	}{
		{metadb.ChannelMigrationPhaseValidate, "validate"},
		{metadb.ChannelMigrationPhaseProbeTarget, "probe_target"},
		{metadb.ChannelMigrationPhaseWriteFence, "write_fence"},
		{metadb.ChannelMigrationPhaseDrainLeader, "drain_leader"},
		{metadb.ChannelMigrationPhaseFinalTargetCatchUp, "final_target_catch_up"},
		{metadb.ChannelMigrationPhaseCommitLeaderMeta, "commit_leader_meta"},
		{metadb.ChannelMigrationPhaseVerifyNewLeader, "verify_new_leader"},
		{metadb.ChannelMigrationPhaseAddLearner, "add_learner"},
		{metadb.ChannelMigrationPhaseBootstrapTarget, "bootstrap_target"},
		{metadb.ChannelMigrationPhaseWarmCatchUp, "warm_catch_up"},
		{metadb.ChannelMigrationPhaseCutoverFence, "cutover_fence"},
		{metadb.ChannelMigrationPhasePromoteAndRemove, "promote_and_remove"},
		{metadb.ChannelMigrationPhaseVerifyMembership, "verify_membership"},
		{metadb.ChannelMigrationPhaseClearFence, "clear_fence"},
		{metadb.ChannelMigrationPhase(255), "unknown"},
	}
	for _, tc := range phases {
		if got := managerChannelMigrationSummary(metadb.ChannelMigrationTask{Phase: tc.phase}).Phase; got != tc.want {
			t.Fatalf("phase %d = %q, want %q", tc.phase, got, tc.want)
		}
	}
}

type fakeChannelMigrationStore struct {
	task                 metadb.ChannelMigrationTask
	err                  error
	leaderTransferReq    channelwrapper.CreateLeaderTransferRequest
	replicaReplaceReq    channelwrapper.CreateReplicaReplaceRequest
	lastListLimit        int
	lastActiveChannelID  ch.ChannelID
	lastGetChannelID     ch.ChannelID
	lastGetTaskID        string
	abortedTask          metadb.ChannelMigrationTask
	abortReason          string
	leaderTransferCalled bool
	replicaReplaceCalled bool
}

func (s *fakeChannelMigrationStore) CreateLeaderTransfer(_ context.Context, req channelwrapper.CreateLeaderTransferRequest) (metadb.ChannelMigrationTask, error) {
	s.leaderTransferCalled = true
	s.leaderTransferReq = req
	if s.err != nil {
		return metadb.ChannelMigrationTask{}, s.err
	}
	return s.task, nil
}

func (s *fakeChannelMigrationStore) CreateReplicaReplace(_ context.Context, req channelwrapper.CreateReplicaReplaceRequest) (metadb.ChannelMigrationTask, error) {
	s.replicaReplaceCalled = true
	s.replicaReplaceReq = req
	if s.err != nil {
		return metadb.ChannelMigrationTask{}, s.err
	}
	return s.task, nil
}

func (s *fakeChannelMigrationStore) GetActive(_ context.Context, id ch.ChannelID) (metadb.ChannelMigrationTask, bool, error) {
	s.lastActiveChannelID = id
	if s.err != nil {
		return metadb.ChannelMigrationTask{}, false, s.err
	}
	return s.task, s.task.TaskID != "", nil
}

func (s *fakeChannelMigrationStore) Get(_ context.Context, id ch.ChannelID, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	s.lastGetChannelID = id
	s.lastGetTaskID = taskID
	if s.err != nil {
		return metadb.ChannelMigrationTask{}, false, s.err
	}
	return s.task, s.task.TaskID != "", nil
}

func (s *fakeChannelMigrationStore) ListActive(_ context.Context, _ ch.ChannelID, limit int) ([]metadb.ChannelMigrationTask, error) {
	s.lastListLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	if s.task.TaskID == "" {
		return nil, nil
	}
	return []metadb.ChannelMigrationTask{s.task}, nil
}

func (s *fakeChannelMigrationStore) Abort(_ context.Context, task metadb.ChannelMigrationTask, reason string) error {
	s.abortedTask = task
	s.abortReason = reason
	return s.err
}
