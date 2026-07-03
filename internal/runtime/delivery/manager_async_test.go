package delivery

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

func TestManagerAsyncSubmitRequiresStart(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 1,
		AsyncWorkers:   1,
	})

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerAsyncSubmitWaitsForQueueSpace(t *testing.T) {
	observer := &recordingManagerObserver{}
	runner := newBlockingManagerRunner()
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          runner,
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted(first) error = %v", err)
	}
	runner.waitStarted(t)
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted(second) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 3, MessageScopedUIDs: []string{"u1"}})
	}()

	select {
	case err := <-result:
		t.Fatalf("SubmitCommitted() returned before queue space: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	runner.releaseRunner()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("SubmitCommitted() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitCommitted() did not return after queue space became available")
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !observer.sawAdmission(DeliveryResultOK) {
		t.Fatalf("admissions = %#v, want ok admission", observer.admissions)
	}
	if observer.sawAdmission(DeliveryResultOverflow) {
		t.Fatalf("admissions = %#v, want no overflow admission", observer.admissions)
	}
}

func TestManagerAsyncSubmitWaitingOnFullQueueRejectsAfterClose(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		runner := newBlockingManagerRunner()
		manager := NewManager(ManagerOptions{
			Planner:        NewPlanner(PlannerOptions{}),
			Runner:         runner,
			AsyncQueueSize: 1,
			AsyncWorkers:   1,
		})
		if err := manager.Start(context.Background()); err != nil {
			t.Fatalf("attempt %d: Start() error = %v", attempt, err)
		}
		if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
			t.Fatalf("attempt %d: SubmitCommitted(first) error = %v", attempt, err)
		}
		runner.waitStarted(t)
		if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2, MessageScopedUIDs: []string{"u1"}}); err != nil {
			t.Fatalf("attempt %d: SubmitCommitted(second) error = %v", attempt, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result := make(chan error, 1)
		go func() {
			result <- manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 3, MessageScopedUIDs: []string{"u1"}})
		}()
		time.Sleep(time.Millisecond)

		stopDone := make(chan error, 1)
		go func() {
			stopDone <- manager.Stop(context.Background())
		}()

		select {
		case err := <-result:
			cancel()
			if !errors.Is(err, ErrManagerClosed) {
				t.Fatalf("attempt %d: SubmitCommitted() error = %v, want ErrManagerClosed", attempt, err)
			}
		case <-time.After(time.Second):
			cancel()
			t.Fatalf("attempt %d: SubmitCommitted() did not return after manager close", attempt)
		}
		runner.releaseRunner()
		select {
		case err := <-stopDone:
			if err != nil {
				t.Fatalf("attempt %d: Stop() error = %v", attempt, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("attempt %d: Stop() did not return after runner release", attempt)
		}
	}
}

func TestManagerAsyncStartAfterStopRejected(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 1,
		AsyncWorkers:   1,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Start() after stop error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerAsyncSubmitReportsOverflowWhenAdmissionContextExpires(t *testing.T) {
	observer := &recordingManagerObserver{}
	runner := newBlockingManagerRunner()
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          runner,
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted(first) error = %v", err)
	}
	runner.waitStarted(t)
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted(second) error = %v", err)
	}
	defer func() {
		runner.releaseRunner()
		if err := manager.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 3, MessageScopedUIDs: []string{"u1"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SubmitCommitted() error = %v, want context deadline", err)
	}
	if !observer.sawAdmission(DeliveryResultOverflow) {
		t.Fatalf("admissions = %#v, want overflow admission", observer.admissions)
	}
}

func TestManagerAsyncStartStopIdempotent(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestManagerAsyncStopRejectsNewSubmits(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerAsyncStopTimeoutClosesAdmissionPermanently(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	runner := newBlockingManagerRunner()
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         runner,
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	runner.waitStarted(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := manager.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Start() after timed-out stop error = %v, want ErrManagerClosed", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2, MessageScopedUIDs: []string{"u1"}}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() after timed-out stop error = %v, want ErrManagerClosed", err)
	}

	runner.releaseRunner()
}

func TestManagerAsyncRunsAcceptedWorkAndReportsTerminal(t *testing.T) {
	observer := &recordingManagerObserver{}
	runner := &countingManagerRunner{}
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          runner,
		AsyncQueueSize:  4,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if runner.count != 1 {
		t.Fatalf("runner count = %d, want 1", runner.count)
	}
	if !observer.sawTerminal(DeliveryResultOK) {
		t.Fatalf("terminals = %#v, want ok terminal", observer.terminals)
	}
}

func TestManagerAsyncReportsRunnerErrorTerminal(t *testing.T) {
	observer := &recordingManagerObserver{}
	wantErr := errors.New("runner failed")
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          recordingManagerRunner{err: wantErr},
		AsyncQueueSize:  4,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !observer.sawTerminal(DeliveryResultError) {
		t.Fatalf("terminals = %#v, want error terminal", observer.terminals)
	}
}

type recordingManagerRunner struct {
	tasks []FanoutTask
	err   error
}

func (r recordingManagerRunner) RunTask(context.Context, FanoutTask) error {
	return r.err
}

type countingManagerRunner struct {
	count int
}

func (r *countingManagerRunner) RunTask(context.Context, FanoutTask) error {
	r.count++
	return nil
}

type blockingManagerRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingManagerRunner() *blockingManagerRunner {
	return &blockingManagerRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingManagerRunner) RunTask(context.Context, FanoutTask) error {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return nil
}

func (r *blockingManagerRunner) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
}

func (r *blockingManagerRunner) releaseRunner() {
	close(r.release)
}

type recordingManagerObserver struct {
	mu         sync.Mutex
	admissions []ManagerAdmissionEvent
	terminals  []ManagerTerminalEvent
}

func (o *recordingManagerObserver) ObserveManagerAdmission(event ManagerAdmissionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.admissions = append(o.admissions, event)
}

func (o *recordingManagerObserver) ObserveManagerTerminal(event ManagerTerminalEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.terminals = append(o.terminals, event)
}

func (o *recordingManagerObserver) sawAdmission(result string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.admissions {
		if event.Result == result {
			return true
		}
	}
	return false
}

func (o *recordingManagerObserver) sawTerminal(result string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.terminals {
		if event.Result == result {
			return true
		}
	}
	return false
}
