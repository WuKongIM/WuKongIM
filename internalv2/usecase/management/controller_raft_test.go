package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
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

type fakeControllerRaftOperator struct {
	called  []uint64
	status  map[uint64]ControllerRaftStatus
	results map[uint64]ControllerRaftCompactionResult
	errors  map[uint64]error
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
