package cluster

import (
	"reflect"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func TestObservationSyncStateBumpsRuntimeRevisionOnlyOnMeaningfulViewChange(t *testing.T) {
	state := newObservationSyncState()

	initial := []controllermeta.SlotRuntimeView{testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(10, 0))}
	state.replaceRuntimeViews(initial)

	if got, want := state.currentRevisions().Runtime, uint64(1); got != want {
		t.Fatalf("currentRevisions().Runtime = %d, want %d", got, want)
	}

	sameMeaning := []controllermeta.SlotRuntimeView{testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(20, 0))}
	state.replaceRuntimeViews(sameMeaning)

	if got, want := state.currentRevisions().Runtime, uint64(1); got != want {
		t.Fatalf("runtime revision after equivalent update = %d, want %d", got, want)
	}

	changed := []controllermeta.SlotRuntimeView{testObservationRuntimeView(1, 2, []uint64{1, 2, 3}, 1, time.Unix(30, 0))}
	state.replaceRuntimeViews(changed)

	if got, want := state.currentRevisions().Runtime, uint64(2); got != want {
		t.Fatalf("runtime revision after meaningful change = %d, want %d", got, want)
	}
}

func TestObservationSyncStateBuildDeltaFallsBackToFullSyncOnRevisionGap(t *testing.T) {
	state := newObservationSyncState()
	state.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
		Tasks:       []controllermeta.ReconcileTask{testObservationTask(1, 1)},
		Nodes:       []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
	})
	state.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(10, 0)),
	})
	state.historyFloor = observationRevisions{
		Assignments: 1,
		Tasks:       1,
		Nodes:       1,
		Runtime:     1,
	}

	resp := state.buildDelta(observationDeltaRequest{
		Revisions: observationRevisions{},
	})

	if !resp.FullSync {
		t.Fatal("buildDelta(...).FullSync = false, want true")
	}
	if got, want := len(resp.Assignments), 1; got != want {
		t.Fatalf("len(resp.Assignments) = %d, want %d", got, want)
	}
	if got, want := len(resp.Tasks), 1; got != want {
		t.Fatalf("len(resp.Tasks) = %d, want %d", got, want)
	}
	if got, want := len(resp.Nodes), 1; got != want {
		t.Fatalf("len(resp.Nodes) = %d, want %d", got, want)
	}
	if got, want := len(resp.RuntimeViews), 1; got != want {
		t.Fatalf("len(resp.RuntimeViews) = %d, want %d", got, want)
	}
}

func TestObservationSyncStateBuildDeltaScopesReturnedSlots(t *testing.T) {
	state := newObservationSyncState()
	state.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{
			testObservationAssignment(1, 1),
			testObservationAssignment(2, 1),
		},
		Tasks: []controllermeta.ReconcileTask{
			testObservationTask(1, 1),
			testObservationTask(2, 1),
		},
	})
	state.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(10, 0)),
		testObservationRuntimeView(2, 2, []uint64{1, 2, 3}, 1, time.Unix(10, 0)),
	})
	before := state.currentRevisions()

	state.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{
			testObservationAssignment(1, 2),
			testObservationAssignment(2, 2),
		},
		Tasks: []controllermeta.ReconcileTask{
			testObservationTask(1, 2),
			testObservationTask(2, 2),
		},
	})
	state.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 3, []uint64{1, 2, 3}, 1, time.Unix(20, 0)),
		testObservationRuntimeView(2, 4, []uint64{1, 2, 3}, 1, time.Unix(20, 0)),
	})

	resp := state.buildDelta(observationDeltaRequest{
		Revisions:      before,
		RequestedSlots: []uint32{2},
	})

	if got, want := slotIDsOfAssignments(resp.Assignments), []uint32{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slotIDsOfAssignments(resp.Assignments) = %v, want %v", got, want)
	}
	if got, want := slotIDsOfTasks(resp.Tasks), []uint32{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slotIDsOfTasks(resp.Tasks) = %v, want %v", got, want)
	}
	if got, want := slotIDsOfRuntimeViews(resp.RuntimeViews), []uint32{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slotIDsOfRuntimeViews(resp.RuntimeViews) = %v, want %v", got, want)
	}
}

func TestObservationSyncStateApplyDeltaUpsertsAndDeletesCaches(t *testing.T) {
	caches := observationAppliedState{
		Assignments: map[uint32]controllermeta.SlotAssignment{
			1: testObservationAssignment(1, 1),
		},
		Tasks: map[uint32]controllermeta.ReconcileTask{
			1: testObservationTask(1, 1),
		},
		Nodes: map[uint64]controllermeta.ClusterNode{
			1: testObservationNode(1, controllermeta.NodeStatusAlive),
		},
		RuntimeViews: map[uint32]controllermeta.SlotRuntimeView{
			1: testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(10, 0)),
		},
		Revisions: observationRevisions{
			Assignments: 1,
			Tasks:       1,
			Nodes:       1,
			Runtime:     1,
		},
	}

	applyObservationDelta(&caches, observationDeltaResponse{
		Revisions: observationRevisions{
			Assignments: 2,
			Tasks:       3,
			Nodes:       2,
			Runtime:     4,
		},
		Assignments: []controllermeta.SlotAssignment{
			testObservationAssignment(2, 2),
		},
		Tasks: []controllermeta.ReconcileTask{
			testObservationTask(2, 2),
		},
		Nodes: []controllermeta.ClusterNode{
			testObservationNode(2, controllermeta.NodeStatusSuspect),
		},
		RuntimeViews: []controllermeta.SlotRuntimeView{
			testObservationRuntimeView(2, 2, []uint64{2, 3, 4}, 2, time.Unix(20, 0)),
		},
		DeletedTasks:        []uint32{1},
		DeletedRuntimeSlots: []uint32{1},
	})

	if _, ok := caches.Tasks[1]; ok {
		t.Fatal("caches.Tasks[1] still exists after delete, want removed")
	}
	if _, ok := caches.RuntimeViews[1]; ok {
		t.Fatal("caches.RuntimeViews[1] still exists after delete, want removed")
	}
	if got, want := caches.Assignments[2].ConfigEpoch, uint64(2); got != want {
		t.Fatalf("caches.Assignments[2].ConfigEpoch = %d, want %d", got, want)
	}
	if got, want := caches.Nodes[2].Status, controllermeta.NodeStatusSuspect; got != want {
		t.Fatalf("caches.Nodes[2].Status = %v, want %v", got, want)
	}
	if got, want := caches.Revisions.Runtime, uint64(4); got != want {
		t.Fatalf("caches.Revisions.Runtime = %d, want %d", got, want)
	}
}

func testObservationAssignment(slotID uint32, epoch uint64) controllermeta.SlotAssignment {
	return controllermeta.SlotAssignment{
		SlotID:         slotID,
		DesiredPeers:   []uint64{1, 2, 3},
		ConfigEpoch:    epoch,
		BalanceVersion: 1,
	}
}

func testObservationTask(slotID uint32, attempt uint32) controllermeta.ReconcileTask {
	return controllermeta.ReconcileTask{
		SlotID:     slotID,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 1,
		TargetNode: 2,
		Attempt:    attempt,
		NextRunAt:  time.Unix(100+int64(attempt), 0),
		Status:     controllermeta.TaskStatusPending,
	}
}

func testObservationNode(nodeID uint64, status controllermeta.NodeStatus) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:          nodeID,
		Addr:            "127.0.0.1:7000",
		Status:          status,
		LastHeartbeatAt: time.Unix(200+int64(nodeID), 0),
		CapacityWeight:  1,
	}
}

func testObservationRuntimeView(slotID uint32, leaderID uint64, peers []uint64, epoch uint64, reportAt time.Time) controllermeta.SlotRuntimeView {
	return controllermeta.SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        append([]uint64(nil), peers...),
		LeaderID:            leaderID,
		HealthyVoters:       uint32(len(peers)),
		HasQuorum:           true,
		ObservedConfigEpoch: epoch,
		LastReportAt:        reportAt,
	}
}

func slotIDsOfAssignments(assignments []controllermeta.SlotAssignment) []uint32 {
	out := make([]uint32, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, assignment.SlotID)
	}
	return out
}

func slotIDsOfTasks(tasks []controllermeta.ReconcileTask) []uint32 {
	out := make([]uint32, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.SlotID)
	}
	return out
}

func slotIDsOfRuntimeViews(views []controllermeta.SlotRuntimeView) []uint32 {
	out := make([]uint32, 0, len(views))
	for _, view := range views {
		out = append(out, view.SlotID)
	}
	return out
}
