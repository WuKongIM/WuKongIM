package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestControllerRaftCompactLogsFansOutToControllerVoters(t *testing.T) {
	generatedAt := time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC)
	operator := &fakeControllerRaftOperator{
		results: map[uint64]ControllerRaftCompactionResult{
			1: {NodeID: 1, Compacted: true, AfterSnapshotIndex: 8},
			2: {NodeID: 2, SkippedReason: "up_to_date", AfterSnapshotIndex: 8},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 3, Roles: []control.Role{control.RoleData}},
				{NodeID: 2, Roles: []control.Role{control.RoleController}},
				{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}},
			},
		}},
		ControllerRaft: operator,
		Now:            func() time.Time { return generatedAt },
	})

	summary, err := app.CompactControllerRaftLogs(context.Background())

	if err != nil {
		t.Fatalf("CompactControllerRaftLogs() error = %v", err)
	}
	if !summary.GeneratedAt.Equal(generatedAt) || summary.Total != 2 || summary.Succeeded != 2 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want two successes at generated time", summary)
	}
	if len(summary.Items) != 2 || summary.Items[0].NodeID != 1 || summary.Items[1].NodeID != 2 {
		t.Fatalf("items = %#v, want node ids 1,2", summary.Items)
	}
	if len(operator.called) != 2 || operator.called[0] != 1 || operator.called[1] != 2 {
		t.Fatalf("called = %#v, want [1 2]", operator.called)
	}
}

func TestControllerRaftCompactLogsPreservesPartialFailure(t *testing.T) {
	operator := &fakeControllerRaftOperator{
		results: map[uint64]ControllerRaftCompactionResult{
			1: {NodeID: 1, Compacted: true, AfterSnapshotIndex: 8},
		},
		errors: map[uint64]error{2: errors.New("target unavailable")},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 2, Roles: []control.Role{control.RoleController}},
				{NodeID: 1, Roles: []control.Role{control.RoleController}},
			},
		}},
		ControllerRaft: operator,
	})

	summary, err := app.CompactControllerRaftLogs(context.Background())

	if err != nil {
		t.Fatalf("CompactControllerRaftLogs() error = %v", err)
	}
	if summary.Succeeded != 1 || summary.Failed != 1 || len(summary.Items) != 2 {
		t.Fatalf("summary = %#v, want one success and one failure", summary)
	}
	if !summary.Items[0].Success || summary.Items[1].Success || summary.Items[1].Error != "target unavailable" {
		t.Fatalf("items = %#v, want partial failure preserved", summary.Items)
	}
}

func TestControllerRaftStatusObserverSeesMembership(t *testing.T) {
	observer := &recordingControllerRaftStatusObserver{}
	app := New(Options{
		ControllerRaft: &fakeControllerRaftOperator{
			status: map[uint64]ControllerRaftStatus{
				1: {NodeID: 1, Voters: []uint64{1, 2, 4}, Learners: []uint64{5}},
			},
		},
		ControllerRaftStatusObserver: observer,
	})

	status, err := app.ControllerRaftStatus(context.Background(), 1)

	if err != nil {
		t.Fatalf("ControllerRaftStatus() error = %v", err)
	}
	if len(status.Voters) != 3 || len(status.Learners) != 1 {
		t.Fatalf("status membership voters=%v learners=%v, want 3 voters and 1 learner", status.Voters, status.Learners)
	}
	if len(observer.statuses) != 1 || len(observer.statuses[0].Voters) != 3 || len(observer.statuses[0].Learners) != 1 {
		t.Fatalf("observer statuses = %#v, want one status with membership", observer.statuses)
	}
}

func TestControllerRaftStatusRejectsInvalidOrUnavailableReadsBeforeObservation(t *testing.T) {
	observer := &recordingControllerRaftStatusObserver{}
	operator := &fakeControllerRaftOperator{errors: map[uint64]error{2: errors.New("raft status unavailable")}}
	app := New(Options{ControllerRaft: operator, ControllerRaftStatusObserver: observer})

	if _, err := app.ControllerRaftStatus(context.Background(), 0); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("zero node status error = %v", err)
	}
	if _, err := app.ControllerRaftStatus(context.Background(), 2); err == nil || err.Error() != "raft status unavailable" {
		t.Fatalf("provider status error = %v", err)
	}
	if len(observer.statuses) != 0 {
		t.Fatalf("failed reads reached observer: %#v", observer.statuses)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.ControllerRaftStatus(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled status error = %v", err)
	}

	var unavailable *App
	if _, err := unavailable.ControllerRaftStatus(context.Background(), 1); !errors.Is(err, ErrControllerRaftOperatorUnavailable) {
		t.Fatalf("unwired status error = %v", err)
	}
}

func TestControllerRaftCompactLogTargetsOneExactNodeAndFailsClosedBeforeDelegation(t *testing.T) {
	operator := &fakeControllerRaftOperator{results: map[uint64]ControllerRaftCompactionResult{
		7: {
			AppliedIndex:        41,
			BeforeSnapshotIndex: 20,
			AfterSnapshotIndex:  41,
			Compacted:           true,
		},
	}}
	app := New(Options{ControllerRaft: operator})

	got, err := app.CompactControllerRaftLog(context.Background(), 7)
	if err != nil {
		t.Fatalf("CompactControllerRaftLog() error = %v", err)
	}
	if got.NodeID != 7 || got.AppliedIndex != 41 || got.BeforeSnapshotIndex != 20 || got.AfterSnapshotIndex != 41 || !got.Compacted {
		t.Fatalf("compaction result = %#v", got)
	}
	if len(operator.called) != 1 || operator.called[0] != 7 {
		t.Fatalf("compaction targets = %#v", operator.called)
	}

	if _, err := app.CompactControllerRaftLog(context.Background(), 0); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("zero node error = %v", err)
	}
	if len(operator.called) != 1 {
		t.Fatalf("invalid node reached operator: %#v", operator.called)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.CompactControllerRaftLog(ctx, 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compaction error = %v", err)
	}
	if len(operator.called) != 1 {
		t.Fatalf("canceled request reached operator: %#v", operator.called)
	}

	var unavailable *App
	if _, err := unavailable.CompactControllerRaftLog(context.Background(), 7); !errors.Is(err, ErrControllerRaftOperatorUnavailable) {
		t.Fatalf("unwired compaction error = %v", err)
	}
}

type fakeControllerRaftOperator struct {
	called  []uint64
	status  map[uint64]ControllerRaftStatus
	results map[uint64]ControllerRaftCompactionResult
	errors  map[uint64]error
}

type recordingControllerRaftStatusObserver struct {
	statuses []ControllerRaftStatus
}

func (o *recordingControllerRaftStatusObserver) ObserveControllerRaftStatus(status ControllerRaftStatus) {
	o.statuses = append(o.statuses, status)
}

func (f *fakeControllerRaftOperator) ControllerRaftStatus(_ context.Context, nodeID uint64) (ControllerRaftStatus, error) {
	if err := f.errors[nodeID]; err != nil {
		return ControllerRaftStatus{}, err
	}
	return f.status[nodeID], nil
}

func (f *fakeControllerRaftOperator) CompactControllerRaftLog(_ context.Context, nodeID uint64) (ControllerRaftCompactionResult, error) {
	f.called = append(f.called, nodeID)
	if err := f.errors[nodeID]; err != nil {
		return ControllerRaftCompactionResult{NodeID: nodeID, Error: err.Error()}, err
	}
	result := f.results[nodeID]
	result.NodeID = nodeID
	return result, nil
}
