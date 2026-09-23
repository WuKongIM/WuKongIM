package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cross the same assignment interface as the HTTP adapter. Channel
// handshakes and synthetic scheduling exercise lifetime ordering without sleeps.
func TestAssignmentConcurrentDuplicatePhaseRunsHookOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newBlockingConnectRunner()
		defer runner.release()
		owner, identity := assignedLifecycle(t, runner)
		completeAssignmentPhase(t, owner, identity, PhasePrepare)
		var wg sync.WaitGroup
		results := make(chan (<-chan error), 2)
		failures := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Go(func() { _, done, err := owner.StartPhase(identity, PhaseConnect); results <- done; failures <- err })
		}
		wg.Wait()
		synctest.Wait()
		require.NoError(t, <-failures)
		require.NoError(t, <-failures)
		first, second := <-results, <-results
		if first == nil {
			first, second = second, first
		}
		require.NotNil(t, first)
		require.Nil(t, second)
		require.Equal(t, int32(1), runner.calls.Load())
		runner.release()
		require.NoError(t, <-first)
		status, done, err := owner.StartPhase(identity, PhaseConnect)
		require.NoError(t, err)
		require.Nil(t, done)
		require.Equal(t, PhaseConnect, status.Phase)
		require.Equal(t, int32(1), runner.calls.Load())
	})
}

func TestAssignmentStopJoinsPhaseAndSharesFinalizer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newDelayedCancelExitRunner()
		defer runner.release()
		owner, identity := assignedLifecycle(t, runner)
		_, phase, err := owner.StartPhase(identity, PhasePrepare)
		require.NoError(t, err)
		<-runner.started
		stop := owner.Stop(identity)
		<-runner.canceled
		synctest.Wait()
		require.Equal(t, PhasePrepare, owner.Status().ActivePhase)
		assertAssignmentPending(t, stop.done)
		assertAssignmentPending(t, runner.ended)
		require.Same(t, stop, owner.Stop(identity))
		// No waiter is needed to keep cleanup alive, and admission stays fenced.
		_, _, err = owner.StartPhase(identity, PhasePrepare)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		_, err = owner.PrepareChannels(context.Background(), identity)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		_, err = owner.Assign(Assignment{RunID: identity.runID, AssignmentID: identity.assignmentID})
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		runner.release()
		<-stop.done
		require.NoError(t, stop.err)
		require.Equal(t, PhaseStopped, stop.status.Phase)
		require.ErrorIs(t, <-phase, context.Canceled)
		<-runner.ended
		require.Same(t, stop, owner.Stop(identity))
	})
}

func TestAssignmentPrepareChannelsFollowsCallerAndJoinsStop(t *testing.T) {
	for _, callerCancels := range []bool{false, true} {
		name := "stop cancels"
		if callerCancels {
			name = "caller cancels"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				runner := newBlockingPrepareChannelsStopper()
				defer runner.release()
				owner, identity := assignedLifecycle(t, runner)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				prepared := make(chan error, 1)
				go func() { _, err := owner.PrepareChannels(ctx, identity); prepared <- err }()
				<-runner.started
				if callerCancels {
					cancel()
					<-runner.canceled
				}
				stop := owner.Stop(identity)
				<-runner.canceled
				synctest.Wait()
				assertAssignmentPending(t, stop.done)
				assertAssignmentPending(t, runner.ended)
				require.NotEqual(t, PhaseStopped, owner.Status().Phase)
				runner.release()
				require.ErrorIs(t, <-prepared, context.Canceled)
				<-stop.done
				require.NoError(t, stop.err)
				<-runner.ended
			})
		})
	}
}

func TestAssignmentRejectsAdmissionUntilTeardownCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newStopAdmissionRunner()
		defer runner.releaseEndAssignment()
		owner, identity := assignedLifecycle(t, runner)
		_, phase, err := owner.StartPhase(identity, PhasePrepare)
		require.NoError(t, err)
		<-runner.firstStarted
		stop := owner.Stop(identity)
		<-runner.endEntered
		_, _, err = owner.StartPhase(identity, PhasePrepare)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		_, err = owner.PrepareChannels(context.Background(), identity)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		next := Assignment{RunID: identity.runID, AssignmentID: "next", WorkerID: "worker-a"}
		_, err = owner.Assign(next)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		_, err = owner.Evidence(identity)
		require.Error(t, err)
		runner.releaseEndAssignment()
		<-stop.done
		require.NoError(t, stop.err)
		require.ErrorIs(t, <-phase, context.Canceled)
		_, err = owner.Assign(next)
		require.NoError(t, err)
		nextIdentity := assignmentIdentity{runID: next.RunID, assignmentID: next.AssignmentID}
		// Delayed old-generation control cannot touch the replacement.
		stale := owner.Stop(identity)
		<-stale.done
		require.ErrorIs(t, stale.err, ErrActiveRunConflict)
		_, _, err = owner.StartPhase(identity, PhasePrepare)
		require.ErrorIs(t, err, ErrActiveRunConflict)
		require.True(t, nextIdentity.matches(owner.Status().Assignment))
		require.Equal(t, PhaseAssigned, owner.Status().Phase)
	})
}

func TestAssignmentFailedTeardownCanRetryWithoutReopeningAdmission(t *testing.T) {
	runner := &assignmentStoppingRunner{err: errors.New("close failed")}
	owner, identity := assignedLifecycle(t, runner)
	first := owner.Stop(identity)
	<-first.done
	require.ErrorContains(t, first.err, "close failed")
	require.Equal(t, PhaseAssigned, owner.Status().Phase)
	_, _, err := owner.StartPhase(identity, PhasePrepare)
	require.ErrorIs(t, err, ErrInvalidPhaseTransition)
	runner.err = nil
	retry := owner.Stop(identity)
	require.NotSame(t, first, retry)
	<-retry.done
	require.NoError(t, retry.err)
	require.Equal(t, PhaseStopped, retry.status.Phase)
	require.Len(t, runner.stoppedRunIDs, 2)
	require.Same(t, retry, owner.Stop(identity))
}

func TestAssignmentRetainsPreCloseEvidenceOnlyForItsGeneration(t *testing.T) {
	runner := &lifecycleSnapshotRunner{lifecycle: LifecycleStatus{
		ActiveConnections: 2500, ReceiveDrain: completeReceiveDrainProof(2500),
		Traffic: TrafficStatus{LogicalSent: 1000, SendACKs: 1000, StableClientMsgNo: true, RetryEvidenceComplete: true},
	}}
	owner, identity := assignedLifecycle(t, runner)
	for _, phase := range []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown} {
		completeAssignmentPhase(t, owner, identity, phase)
	}
	stop := owner.Stop(identity)
	<-stop.done
	require.NoError(t, stop.err)
	require.True(t, stop.status.Lifecycle.TerminalPreClose)
	runner.lifecycle = LifecycleStatus{ActiveConnections: 17, Traffic: TrafficStatus{Remaining: 99}}
	status := owner.Status()
	require.Equal(t, 2500, status.Lifecycle.ActiveConnections)
	require.Zero(t, status.Lifecycle.Traffic.Remaining)
	require.Same(t, stop, owner.Stop(identity))
	_, err := owner.Evidence(identity)
	require.NoError(t, err)
	_, err = owner.Assign(Assignment{RunID: identity.runID, AssignmentID: "next", WorkerID: "worker-a"})
	require.NoError(t, err)
	require.False(t, owner.Status().Lifecycle.TerminalPreClose)
	require.Equal(t, 17, owner.Status().Lifecycle.ActiveConnections)
	_, err = owner.Evidence(identity)
	require.ErrorIs(t, err, ErrActiveRunConflict)
}

func TestAssignmentStoppedGenerationCannotReactivate(t *testing.T) {
	runner := &assignmentStartRecorder{}
	owner, identity := assignedLifecycle(t, runner)
	assignment := owner.Status().Assignment
	stop := owner.Stop(identity)
	<-stop.done
	require.NoError(t, stop.err)
	_, err := owner.Assign(assignment)
	require.ErrorIs(t, err, ErrActiveRunConflict)
	require.Len(t, runner.started, 1)
	require.Equal(t, PhaseStopped, owner.Status().Phase)
}

func TestAssignmentTerminalCutAndSealShareGenerationOwnership(t *testing.T) {
	for _, mode := range []string{"complete", "seal failure", "missing sealer"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := newTerminalCutTestRunner()
				var runner WorkloadRunner = base
				var failing *terminalReceiveSealFailureRunner
				if mode == "seal failure" {
					failing = &terminalReceiveSealFailureRunner{terminalCutTestRunner: base, sealErr: errors.New("private failure")}
					runner = failing
				}
				if mode == "missing sealer" {
					runner = &terminalCutNoSealerRunner{inner: base}
				}
				owner := newAssignmentLifecycle("", runner)
				assignment := terminalCutTestAssignment(time.Second)
				_, err := owner.Assign(assignment)
				require.NoError(t, err)
				identity := assignmentIdentity{runID: assignment.RunID, assignmentID: assignment.AssignmentID}
				for _, phase := range []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun} {
					completeAssignmentPhase(t, owner, identity, phase)
				}
				_, cooldown, err := owner.StartPhase(identity, PhaseCooldown)
				require.NoError(t, err)
				synctest.Wait()
				require.True(t, owner.Status().Lifecycle.TerminalCutReady)
				request := terminalCutRequestForTest(assignment, time.Now().UTC())
				stale := request
				stale.AssignmentID = "stale"
				_, err = owner.AcknowledgeTerminalCut(stale)
				require.ErrorIs(t, err, ErrActiveRunConflict)
				binding, err := owner.AcknowledgeTerminalCut(request)
				require.NoError(t, err)
				retry, err := owner.AcknowledgeTerminalCut(request)
				require.NoError(t, err)
				require.Equal(t, binding, retry)
				changed := request
				changed.ProductMetricsSHA256 = "different"
				_, err = owner.AcknowledgeTerminalCut(changed)
				require.ErrorIs(t, err, ErrTerminalCutAlreadyAcknowledged)
				require.NoError(t, <-cooldown)
				stop := owner.Stop(identity)
				<-stop.done
				if mode == "seal failure" {
					require.ErrorIs(t, stop.err, errTerminalReceiveSealFailed)
					require.Equal(t, int32(1), failing.endCalls.Load())
					require.Equal(t, int32(1), failing.sealCalls.Load())
					require.Same(t, stop, owner.Stop(identity))
				} else {
					require.NoError(t, stop.err)
				}
				status := owner.Status()
				require.Equal(t, PhaseStopped, status.Phase)
				if mode == "complete" {
					require.NotNil(t, status.Lifecycle)
					require.True(t, status.Lifecycle.TerminalPreClose)
				} else {
					require.True(t, status.Lifecycle == nil || !status.Lifecycle.TerminalPreClose)
				}
			})
		})
	}
}

// A delayed cleanup is an internal seam invariant that cannot be scheduled from
// HTTP. Keep this focused test beside the owner rather than reaching through Server.
func TestAssignmentOldTaskCannotClearNewCancellation(t *testing.T) {
	owner := newAssignmentLifecycle("", nil)
	first := owner.storeLifecycleTask("run-a", "generation-a", lifecycleTaskPhase, PhasePrepare, func() {}, make(chan struct{}))
	canceled := make(chan struct{})
	done := make(chan struct{})
	second := owner.storeLifecycleTask("run-a", "generation-a", lifecycleTaskPrepareChannels, "", func() { close(canceled) }, done)
	require.NotEqual(t, first, second)
	owner.clearLifecycleTask(first)
	require.Equal(t, (<-chan struct{})(done), owner.cancelActiveLifecycleTask(assignmentIdentity{runID: "run-a", assignmentID: "generation-a"}))
	<-canceled
}

func assignedLifecycle(t *testing.T, runner WorkloadRunner) (*assignmentLifecycle, assignmentIdentity) {
	t.Helper()
	owner := newAssignmentLifecycle("", runner)
	identity := assignmentIdentity{runID: "run-a", assignmentID: "generation-a"}
	_, err := owner.Assign(Assignment{RunID: identity.runID, AssignmentID: identity.assignmentID, WorkerID: "worker-a"})
	require.NoError(t, err)
	return owner, identity
}

func completeAssignmentPhase(t *testing.T, owner *assignmentLifecycle, identity assignmentIdentity, phase Phase) {
	t.Helper()
	_, done, err := owner.StartPhase(identity, phase)
	require.NoError(t, err)
	require.NotNil(t, done)
	require.NoError(t, <-done)
}

func assertAssignmentPending(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("operation completed before its owned work was released")
	default:
	}
}

func TestAssignmentCannotUsePhaseAdmissionToBypassTeardown(t *testing.T) {
	runner := &assignmentStoppingRunner{}
	owner, identity := assignedLifecycle(t, runner)
	for _, phase := range []Phase{PhaseStopped, PhaseIdle, PhaseAssigned, "unknown"} {
		_, done, err := owner.StartPhase(identity, phase)
		require.ErrorIs(t, err, ErrInvalidPhaseTransition)
		require.Nil(t, done)
	}
	require.Equal(t, PhaseAssigned, owner.Status().Phase)
	require.Empty(t, runner.stoppedRunIDs)
	stop := owner.Stop(identity)
	<-stop.done
	require.NoError(t, stop.err)
	require.Len(t, runner.stoppedRunIDs, 1)
}
