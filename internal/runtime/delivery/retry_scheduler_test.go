package delivery

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRetrySchedulerEnqueuesRetryableTaskAndRetriesInBackground(t *testing.T) {
	runner := newScriptedRetryRunner(ErrRetryableFanoutTask, nil)
	scheduler := NewRetryScheduler(RetrySchedulerOptions{
		Runner:      runner,
		Capacity:    2,
		MaxAttempts: 3,
		Backoff:     0,
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer scheduler.Stop(context.Background())

	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 7}, Attempt: 1}
	if err := scheduler.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	waitForRetryTasks(t, runner, 2)
	tasks := runner.Tasks()
	if tasks[1].Attempt != 2 || tasks[1].Envelope.MessageID != 1 {
		t.Fatalf("retry task = %#v, want attempt 2 message 1", tasks[1])
	}
}

func TestRetrySchedulerReturnsQueueFullWhenRetryQueueIsBounded(t *testing.T) {
	runner := newScriptedRetryRunner(ErrRetryableFanoutTask, ErrRetryableFanoutTask)
	scheduler := NewRetryScheduler(RetrySchedulerOptions{Runner: runner, Capacity: 1, MaxAttempts: 3})

	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Attempt: 1}
	if err := scheduler.RunTask(context.Background(), task); err != nil {
		t.Fatalf("first RunTask() error = %v", err)
	}
	if err := scheduler.RunTask(context.Background(), task); !errors.Is(err, ErrRetryQueueFull) {
		t.Fatalf("second RunTask() error = %v, want ErrRetryQueueFull", err)
	}
	if got := scheduler.QueueDepth(); got != 1 {
		t.Fatalf("QueueDepth() = %d, want 1", got)
	}
}

func TestRetrySchedulerDoesNotEnqueueNonRetryableErrors(t *testing.T) {
	wantErr := errors.New("push codec failed")
	runner := newScriptedRetryRunner(wantErr)
	scheduler := NewRetryScheduler(RetrySchedulerOptions{Runner: runner, Capacity: 1, MaxAttempts: 3})

	err := scheduler.RunTask(context.Background(), FanoutTask{Envelope: Envelope{MessageID: 1}, Attempt: 1})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunTask() error = %v, want %v", err, wantErr)
	}
	if got := scheduler.QueueDepth(); got != 0 {
		t.Fatalf("QueueDepth() = %d, want 0", got)
	}
}

func TestRetrySchedulerNarrowsRetryablePushRoutesToScopedUIDs(t *testing.T) {
	runner := newScriptedRetryRunner(newRetryablePushRoutesError([]Route{
		{UID: "u2", OwnerNodeID: 1, SessionID: 20},
		{UID: "u3", OwnerNodeID: 1, SessionID: 30},
	}), nil)
	scheduler := NewRetryScheduler(RetrySchedulerOptions{
		Runner:      runner,
		Capacity:    2,
		MaxAttempts: 3,
		Backoff:     0,
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer scheduler.Stop(context.Background())

	task := FanoutTask{Envelope: Envelope{
		MessageID:         1,
		MessageScopedUIDs: []string{"u1", "u2", "u3"},
	}, Attempt: 1}
	if err := scheduler.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	waitForRetryTasks(t, runner, 2)
	got := runner.Tasks()[1].Envelope.MessageScopedUIDs
	if want := []string{"u2", "u3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry scoped UIDs = %#v, want %#v", got, want)
	}
}

func TestRetrySchedulerStopDrainsQueuedRetryWithoutWaitingBackoff(t *testing.T) {
	runner := newScriptedRetryRunner(ErrRetryableFanoutTask, nil)
	scheduler := NewRetryScheduler(RetrySchedulerOptions{
		Runner:      runner,
		Capacity:    2,
		MaxAttempts: 3,
		Backoff:     time.Hour,
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Attempt: 1}
	if err := scheduler.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := len(runner.Tasks()); got < 2 {
		t.Fatalf("tasks = %d, want retry attempt during Stop", got)
	}
}

type scriptedRetryRunner struct {
	mu    sync.Mutex
	errs  []error
	tasks []FanoutTask
}

func newScriptedRetryRunner(errs ...error) *scriptedRetryRunner {
	return &scriptedRetryRunner{errs: errs}
}

func (r *scriptedRetryRunner) RunTask(_ context.Context, task FanoutTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

func (r *scriptedRetryRunner) Tasks() []FanoutTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FanoutTask(nil), r.tasks...)
}

func waitForRetryTasks(t *testing.T, runner *scriptedRetryRunner, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(runner.Tasks()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tasks = %d, want at least %d", len(runner.Tasks()), want)
}
