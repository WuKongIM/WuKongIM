package delivery

import (
	"context"
	"errors"
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

func TestManagerAsyncQueueFullReportsAdmission(t *testing.T) {
	observer := &recordingManagerObserver{}
	started := make(chan struct{}, 1)
	block := make(chan struct{})
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          blockingManagerRunner{started: started, block: block},
		AsyncQueueSize:  1,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer manager.Stop(context.Background())
	defer close(block)

	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1}); err != nil {
		t.Fatalf("first SubmitCommitted() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async manager worker to start")
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2}); err != nil {
		t.Fatalf("second SubmitCommitted() error = %v", err)
	}
	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 3})
	if !errors.Is(err, ErrManagerQueueFull) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerQueueFull", err)
	}
	if !observer.sawAdmission(DeliveryResultOverflow) {
		t.Fatalf("admissions = %#v, want overflow admission", observer.admissions)
	}
}

type recordingManagerRunner struct {
	tasks []FanoutTask
	err   error
}

func (r recordingManagerRunner) RunTask(context.Context, FanoutTask) error {
	return r.err
}

type blockingManagerRunner struct {
	started chan<- struct{}
	block   <-chan struct{}
}

func (r blockingManagerRunner) RunTask(context.Context, FanoutTask) error {
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	<-r.block
	return nil
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
