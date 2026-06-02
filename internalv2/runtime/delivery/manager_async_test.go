package delivery

import (
	"context"
	"errors"
	"runtime"
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

func TestManagerAsyncQueueFullReportsAdmission(t *testing.T) {
	observer := &recordingManagerObserver{}
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          recordingManagerRunner{},
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer manager.Stop(context.Background())

	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1}); err != nil {
		t.Fatalf("first SubmitCommitted() error = %v", err)
	}
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

type recordingManagerRunner struct {
	tasks []FanoutTask
	err   error
}

func (r recordingManagerRunner) RunTask(context.Context, FanoutTask) error {
	return r.err
}

type recordingManagerObserver struct {
	admissions []ManagerAdmissionEvent
	terminals  []ManagerTerminalEvent
}

func (o *recordingManagerObserver) ObserveManagerAdmission(event ManagerAdmissionEvent) {
	o.admissions = append(o.admissions, event)
}

func (o *recordingManagerObserver) ObserveManagerTerminal(event ManagerTerminalEvent) {
	o.terminals = append(o.terminals, event)
}

func (o *recordingManagerObserver) sawAdmission(result string) bool {
	for _, event := range o.admissions {
		if event.Result == result {
			return true
		}
	}
	return false
}
