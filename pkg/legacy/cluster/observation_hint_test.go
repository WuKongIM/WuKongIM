package cluster

import (
	"reflect"
	"testing"
)

func TestObservationHintCodecRoundTrip(t *testing.T) {
	body := encodeObservationHint(observationHint{
		LeaderID:         9,
		LeaderGeneration: 3,
		Revisions: observationRevisions{
			Assignments: 1,
			Tasks:       2,
			Nodes:       3,
			Runtime:     4,
		},
		AffectedSlots: []uint32{7, 8},
		NeedFullSync:  true,
	})

	hint, err := decodeObservationHint(body)
	if err != nil {
		t.Fatalf("decodeObservationHint() error = %v", err)
	}
	if !reflect.DeepEqual(hint, observationHint{
		LeaderID:         9,
		LeaderGeneration: 3,
		Revisions: observationRevisions{
			Assignments: 1,
			Tasks:       2,
			Nodes:       3,
			Runtime:     4,
		},
		AffectedSlots: []uint32{7, 8},
		NeedFullSync:  true,
	}) {
		t.Fatalf("decoded hint = %+v", hint)
	}
}

func TestObservationWakeStateRejectsStaleLeaderGeneration(t *testing.T) {
	state := newObservationWakeState()

	if ok := state.observeHint(1, observationHint{
		LeaderID:         1,
		LeaderGeneration: 3,
		Revisions:        observationRevisions{Assignments: 1},
		AffectedSlots:    []uint32{1},
	}); !ok {
		t.Fatal("observeHint(new leader generation) = false, want true")
	}

	if ok := state.observeHint(1, observationHint{
		LeaderID:         1,
		LeaderGeneration: 2,
		Revisions:        observationRevisions{Assignments: 2},
		AffectedSlots:    []uint32{2},
		NeedFullSync:     true,
	}); ok {
		t.Fatal("observeHint(stale generation) = true, want false")
	}

	snapshot := state.snapshot()
	if !snapshot.Pending {
		t.Fatal("snapshot.Pending = false, want true")
	}
	if got, want := snapshot.Hint.LeaderGeneration, uint64(3); got != want {
		t.Fatalf("snapshot.Hint.LeaderGeneration = %d, want %d", got, want)
	}
	if got, want := snapshot.Hint.AffectedSlots, []uint32{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot.Hint.AffectedSlots = %v, want %v", got, want)
	}
}

func TestObservationWakeStateCoalescesDuplicateHints(t *testing.T) {
	state := newObservationWakeState()

	hint := observationHint{
		LeaderID:         1,
		LeaderGeneration: 4,
		Revisions:        observationRevisions{Runtime: 1},
		AffectedSlots:    []uint32{1},
	}
	if ok := state.observeHint(1, hint); !ok {
		t.Fatal("observeHint(first) = false, want true")
	}
	if ok := state.observeHint(1, hint); ok {
		t.Fatal("observeHint(duplicate) = true, want false")
	}
	if ok := state.observeHint(1, observationHint{
		LeaderID:         1,
		LeaderGeneration: 4,
		Revisions:        observationRevisions{Runtime: 2},
		AffectedSlots:    []uint32{2},
	}); !ok {
		t.Fatal("observeHint(newer) = false, want true")
	}

	snapshot := state.snapshot()
	if !snapshot.Pending {
		t.Fatal("snapshot.Pending = false, want true")
	}
	if got, want := snapshot.Hint.Revisions.Runtime, uint64(2); got != want {
		t.Fatalf("snapshot.Hint.Revisions.Runtime = %d, want %d", got, want)
	}
	if got, want := snapshot.Hint.AffectedSlots, []uint32{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot.Hint.AffectedSlots = %v, want %v", got, want)
	}
}

func TestClusterHandleObservationHintMarksWakePending(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{NodeID: 2},
		observationResources: observationResources{
			wakeState: newObservationWakeState(),
		},
	}
	client := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	client.setLeader(1)
	cluster.controllerClient = client

	if ok := cluster.handleObservationHint(observationHint{
		LeaderID:         1,
		LeaderGeneration: 5,
		Revisions:        observationRevisions{Tasks: 1},
		AffectedSlots:    []uint32{7},
	}); !ok {
		t.Fatal("handleObservationHint() = false, want true")
	}

	snapshot := cluster.wakeState.snapshot()
	if !snapshot.Pending {
		t.Fatal("snapshot.Pending = false, want true")
	}
	if got, want := snapshot.Hint.AffectedSlots, []uint32{7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot.Hint.AffectedSlots = %v, want %v", got, want)
	}
}
