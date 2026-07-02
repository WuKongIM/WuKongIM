package management

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
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

type fakeChannelMigrationStore struct {
	task                 metadb.ChannelMigrationTask
	err                  error
	leaderTransferReq    channelwrapper.CreateLeaderTransferRequest
	replicaReplaceReq    channelwrapper.CreateReplicaReplaceRequest
	lastListLimit        int
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

func (s *fakeChannelMigrationStore) GetActive(context.Context, ch.ChannelID) (metadb.ChannelMigrationTask, bool, error) {
	if s.err != nil {
		return metadb.ChannelMigrationTask{}, false, s.err
	}
	return s.task, s.task.TaskID != "", nil
}

func (s *fakeChannelMigrationStore) Get(context.Context, ch.ChannelID, string) (metadb.ChannelMigrationTask, bool, error) {
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

func (s *fakeChannelMigrationStore) Abort(context.Context, metadb.ChannelMigrationTask, string) error {
	return s.err
}
