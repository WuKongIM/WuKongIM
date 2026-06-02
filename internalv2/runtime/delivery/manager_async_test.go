package delivery

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

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

func TestManagerAsyncBoundedAdmissionReportsQueueFull(t *testing.T) {
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
	manager.async.queue <- managerCommand{envelope: Envelope{MessageID: 1}}
	manager.async.mu.Unlock()

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2})
	if !errors.Is(err, ErrManagerQueueFull) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerQueueFull", err)
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

	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := manager.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Start() while closing error = %v, want ErrManagerClosed", err)
	}

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
