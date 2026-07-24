package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestExecutorSubmitAfterStopReturnsStopped(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := executor.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err = executor.Submit(&serviceTask{})
	if !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Submit(after stop) error = %v, want %v", err, core.ErrStopped)
	}
}

func TestExecutorStopIsIdempotent(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	if err := executor.Stop(); err != nil {
		t.Fatalf("Stop() first error = %v, want nil", err)
	}
	if err := executor.Stop(); err != nil {
		t.Fatalf("Stop() second error = %v, want nil", err)
	}

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- executor.Stop()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Stop() error = %v, want nil", err)
		}
	}
}

func TestExecutorStopKeepsPoolOwnershipUntilWorkerExits(t *testing.T) {
	baseline := goruntimeregistry.Default().Baseline()
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := executor.Submit(&serviceTask{runFunc: func() {
		close(entered)
		<-release
	}}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	<-entered
	if err := executor.Stop(); err == nil {
		t.Fatal("Stop() error = nil, want bounded release timeout")
	}
	task := executorTaskSnapshot(t)
	if task.Active <= baseline.Active(goruntimeregistry.TaskTransportRPCExecutor) || task.BusyTasks != 1 {
		t.Fatalf("timed-out executor snapshot = %+v, want live worker ownership", task)
	}
	close(release)
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := goruntimeregistry.Default().Group(goruntimeregistry.ModuleTransport).WaitFrom(waitCtx, baseline); err != nil {
		t.Fatalf("transport group WaitFrom() error = %v", err)
	}
}

func TestExecutorRunsSubmittedTask(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	done := make(chan struct{})
	err = executor.Submit(&serviceTask{
		runFunc: func() {
			close(done)
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for executor task")
	}
}

func TestExecutorSubmitNilTaskReturnsNil(t *testing.T) {
	observer := &executorTestObserver{events: make(chan core.Event, 1)}
	executor, err := NewExecutor(1, observer)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	if err := executor.Submit(nil); err != nil {
		t.Fatalf("Submit(nil) error = %v, want nil", err)
	}

	select {
	case event := <-observer.events:
		t.Fatalf("Submit(nil) emitted event = %+v, want none", event)
	case <-time.After(100 * time.Millisecond):
	}
}

type executorTestObserver struct {
	events chan core.Event
}

func (o *executorTestObserver) ObserveTransport(event core.Event) {
	o.events <- event
}

func executorTaskSnapshot(t *testing.T) goruntimeregistry.TaskSnapshot {
	t.Helper()
	for _, module := range goruntimeregistry.Default().Snapshot().Modules {
		for _, task := range module.Tasks {
			if task.Task == goruntimeregistry.TaskTransportRPCExecutor {
				return task
			}
		}
	}
	t.Fatalf("task %q missing from default registry", goruntimeregistry.TaskTransportRPCExecutor)
	return goruntimeregistry.TaskSnapshot{}
}
