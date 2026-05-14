package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateRejectsOutOfOrderPhaseTransition(t *testing.T) {
	state := NewState("")
	require.NoError(t, state.Assign(Assignment{RunID: "run-a", WorkerID: "worker-a"}))

	err := state.Transition(PhaseRun)

	require.ErrorIs(t, err, ErrInvalidPhaseTransition)
	require.Equal(t, PhaseAssigned, state.Status().Phase)
}

func TestStateTransitionsPhasesMonotonically(t *testing.T) {
	state := NewState("")
	require.NoError(t, state.Assign(Assignment{RunID: "run-a", WorkerID: "worker-a"}))

	for _, phase := range []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown, PhaseStopped} {
		require.NoError(t, state.Transition(phase), phase)
		require.Equal(t, phase, state.Status().Phase)
	}
}

func TestStateRejectsDifferentActiveRunUntilStopped(t *testing.T) {
	state := NewState("")
	require.NoError(t, state.Assign(Assignment{RunID: "run-a", WorkerID: "worker-a"}))

	err := state.Assign(Assignment{RunID: "run-b", WorkerID: "worker-a"})

	require.ErrorIs(t, err, ErrActiveRunConflict)
	require.NoError(t, state.Stop())
	require.NoError(t, state.Assign(Assignment{RunID: "run-b", WorkerID: "worker-a"}))
	require.Equal(t, "run-b", state.Status().Assignment.RunID)
}

func TestStateStopSetsStoppedWithoutAssignment(t *testing.T) {
	state := NewState("")

	require.NoError(t, state.Stop())

	require.Equal(t, PhaseStopped, state.Status().Phase)
}

func TestStatePersistsAssignmentWhenWorkDirIsSet(t *testing.T) {
	workDir := t.TempDir()
	state := NewState(workDir)

	require.NoError(t, state.Assign(Assignment{RunID: "run-a", WorkerID: "worker-a"}))

	data, err := os.ReadFile(filepath.Join(workDir, "current-run.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"run_id":"run-a","worker_id":"worker-a"}`, string(data))
}
