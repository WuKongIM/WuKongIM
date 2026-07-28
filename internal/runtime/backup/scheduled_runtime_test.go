package backup

import (
	"context"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

type scheduledRuntimeStateForTest struct {
	mu    sync.Mutex
	state backupcontract.SystemState
}

func (s *scheduledRuntimeStateForTest) Evaluate(context.Context, time.Duration) error {
	return nil
}

func (s *scheduledRuntimeStateForTest) State(context.Context) (backupcontract.SystemState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *scheduledRuntimeStateForTest) requestBackupCancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ActiveBackup.CancelRequested = true
}

type scheduledRuntimeLeadershipForTest struct{}

func (scheduledRuntimeLeadershipForTest) NodeID() uint64 { return 1 }
func (scheduledRuntimeLeadershipForTest) BackupControllerLeaderID() uint64 {
	return 1
}
func (scheduledRuntimeLeadershipForTest) BackupControllerFence(context.Context) (uint64, uint64, error) {
	return 1, 7, nil
}

type idleRuntimeRunnerForTest struct{}

func (idleRuntimeRunnerForTest) RunOnce(context.Context) (bool, error) {
	return false, nil
}

type blockingRuntimeRunnerForTest struct {
	started chan struct{}
	done    chan struct{}
}

func (r *blockingRuntimeRunnerForTest) RunOnce(ctx context.Context) (bool, error) {
	close(r.started)
	<-ctx.Done()
	close(r.done)
	return false, ctx.Err()
}

func TestScheduledRuntimePropagatesNewCancellationToActiveIO(t *testing.T) {
	state := &scheduledRuntimeStateForTest{
		state: backupcontract.SystemState{
			ActiveBackup: &backupcontract.BackupJob{
				ID: "backup-cancel-in-flight",
			},
		},
	}
	runner := &blockingRuntimeRunnerForTest{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	runtime, err := NewScheduledRuntime(ScheduledRuntimeOptions{
		Scheduled:  state,
		State:      state,
		Runner:     runner,
		Restore:    idleRuntimeRunnerForTest{},
		Leadership: scheduledRuntimeLeadershipForTest{},
		Tick:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewScheduledRuntime() error = %v", err)
	}

	advanceDone := make(chan struct{})
	go func() {
		defer close(advanceDone)
		runtime.advance(context.Background())
	}()
	<-runner.started
	state.requestBackupCancel()

	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight runner did not receive cancellation")
	}
	<-advanceDone
}
