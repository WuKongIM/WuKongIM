package delivery

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
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
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          recordingManagerRunner{},
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})

	// This lower-level admission test keeps worker lifecycle out of the assertion
	// so queue pressure remains deterministic.
	manager.async.mu.Lock()
	manager.async.state = managerStateOpen
	fillManagerAsyncQueueLocked(t, manager, Envelope{MessageID: 1})
	manager.async.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 2})
	}()

	select {
	case err := <-result:
		t.Fatalf("SubmitCommitted() returned before queue space: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	<-manager.async.queue
	manager.async.releaseSlot()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("SubmitCommitted() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitCommitted() did not return after queue space became available")
	}
	select {
	case cmd := <-manager.async.queue:
		if cmd.envelope.MessageID != 2 {
			t.Fatalf("queued message id = %d, want 2", cmd.envelope.MessageID)
		}
	default:
		t.Fatal("SubmitCommitted() returned without admitting the command")
	}
	if !observer.sawAdmission(DeliveryResultOK) {
		t.Fatalf("admissions = %#v, want ok admission", observer.admissions)
	}
	if observer.sawAdmission(DeliveryResultOverflow) {
		t.Fatalf("admissions = %#v, want no overflow admission", observer.admissions)
	}
}

func TestManagerAsyncSubmitWaitingOnFullQueueRejectsAfterClose(t *testing.T) {
	for attempt := 0; attempt < 100; attempt++ {
		manager := NewManager(ManagerOptions{
			Planner:        NewPlanner(PlannerOptions{}),
			Runner:         recordingManagerRunner{},
			AsyncQueueSize: 1,
			AsyncWorkers:   1,
		})

		acceptDone := make(chan struct{})
		manager.async.mu.Lock()
		manager.async.state = managerStateOpen
		manager.async.acceptDone = acceptDone
		fillManagerAsyncQueueLocked(t, manager, Envelope{MessageID: 1})
		manager.async.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result := make(chan error, 1)
		go func() {
			result <- manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 2})
		}()
		time.Sleep(time.Millisecond)

		manager.async.mu.Lock()
		manager.async.state = managerStateClosing
		manager.async.mu.Unlock()
		close(acceptDone)
		<-manager.async.queue
		manager.async.releaseSlot()

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
		select {
		case cmd := <-manager.async.queue:
			t.Fatalf("attempt %d: late queued message id = %d", attempt, cmd.envelope.MessageID)
		default:
		}
	}
}

func TestManagerAsyncSubmitRejectsClosedLifecycleBeforeFastPathAdmission(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 1,
		AsyncWorkers:   1,
	})
	acceptDone := make(chan struct{})
	close(acceptDone)
	manager.async.mu.Lock()
	manager.async.state = managerStateOpen
	manager.async.acceptDone = acceptDone
	manager.async.mu.Unlock()

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
	select {
	case cmd := <-manager.async.queue:
		t.Fatalf("late queued message id = %d", cmd.envelope.MessageID)
	default:
	}
}

func TestManagerAsyncSubmitReportsOverflowWhenAdmissionContextExpires(t *testing.T) {
	observer := &recordingManagerObserver{}
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          recordingManagerRunner{},
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})

	// This lower-level admission test keeps worker lifecycle out of the assertion
	// so queue overflow remains deterministic after workers execute immediately.
	manager.async.mu.Lock()
	manager.async.state = managerStateOpen
	fillManagerAsyncQueueLocked(t, manager, Envelope{MessageID: 1})
	manager.async.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := manager.SubmitCommitted(ctx, messageevents.MessageCommitted{MessageID: 2})
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

func TestManagerAsyncStopTimeoutKeepsClosingUntilWorkersExit(t *testing.T) {
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
		t.Fatalf("Start() while closing error = %v, want ErrManagerClosed", err)
	}

	runner.releaseRunner()
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() after workers exit error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("final Stop() error = %v", err)
	}
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

func fillManagerAsyncQueueLocked(t *testing.T, manager *Manager, env Envelope) {
	t.Helper()
	select {
	case <-manager.async.slots:
	default:
		t.Fatal("manager async queue has no free slot")
	}
	select {
	case manager.async.queue <- managerCommand{envelope: env}:
	default:
		manager.async.releaseSlot()
		t.Fatal("manager async queue is full")
	}
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
