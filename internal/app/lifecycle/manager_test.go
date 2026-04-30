package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type contextKey string

type fakeComponent struct {
	name     string
	startErr error
	stopErr  error
	calls    *[]string
}

func (c *fakeComponent) Name() string { return c.name }

func (c *fakeComponent) Start(context.Context) error {
	*c.calls = append(*c.calls, "start:"+c.name)
	return c.startErr
}

func (c *fakeComponent) Stop(ctx context.Context) error {
	call := "stop:" + c.name
	if marker, ok := ctx.Value(contextKey("marker")).(string); ok {
		call += ":" + marker
	}
	*c.calls = append(*c.calls, call)
	return c.stopErr
}

func TestManagerStartsInOrderAndStopsReverse(t *testing.T) {
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", calls: &calls},
		&fakeComponent{name: "three", calls: &calls},
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	want := []string{"start:one", "start:two", "start:three", "stop:three", "stop:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerRollsBackOnlyStartedComponentsOnStartFailure(t *testing.T) {
	startErr := errors.New("start failed")
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", startErr: startErr, calls: &calls},
		&fakeComponent{name: "three", calls: &calls},
	)

	err := manager.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("start err = %v, want %v", err, startErr)
	}

	want := []string{"start:one", "start:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerRollbackAggregatesStopErrors(t *testing.T) {
	startErr := errors.New("start failed")
	stopTwoErr := errors.New("stop two failed")
	stopOneErr := errors.New("stop one failed")
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", stopErr: stopOneErr, calls: &calls},
		&fakeComponent{name: "two", stopErr: stopTwoErr, calls: &calls},
		&fakeComponent{name: "three", startErr: startErr, calls: &calls},
	)

	err := manager.Start(context.Background())
	if err == nil {
		t.Fatal("start succeeded, want error")
	}
	if !errors.Is(err, startErr) || !errors.Is(err, stopTwoErr) || !errors.Is(err, stopOneErr) {
		t.Fatalf("start err = %v, want joined start and rollback errors", err)
	}
	assertJoinedErrors(t, err, []error{startErr, stopTwoErr, stopOneErr})

	want := []string{"start:one", "start:two", "start:three", "stop:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerStartUsesSeparateRollbackContext(t *testing.T) {
	startErr := errors.New("start failed")
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", startErr: startErr, calls: &calls},
	)
	startCtx := context.WithValue(context.Background(), contextKey("marker"), "start")
	rollbackCtx := context.WithValue(context.Background(), contextKey("marker"), "rollback")

	err := manager.StartWithRollbackContext(startCtx, rollbackCtx)

	if !errors.Is(err, startErr) {
		t.Fatalf("start err = %v, want %v", err, startErr)
	}
	want := []string{"start:one", "start:two", "stop:one:rollback"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerStopAggregatesErrorsInReverseOrder(t *testing.T) {
	stopThreeErr := errors.New("stop three failed")
	stopOneErr := errors.New("stop one failed")
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", stopErr: stopOneErr, calls: &calls},
		&fakeComponent{name: "two", calls: &calls},
		&fakeComponent{name: "three", stopErr: stopThreeErr, calls: &calls},
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	err := manager.Stop(context.Background())
	if err == nil {
		t.Fatal("stop succeeded, want error")
	}
	if !errors.Is(err, stopThreeErr) || !errors.Is(err, stopOneErr) {
		t.Fatalf("stop err = %v, want joined stop errors", err)
	}
	assertJoinedErrors(t, err, []error{stopThreeErr, stopOneErr})

	want := []string{"start:one", "start:two", "start:three", "stop:three", "stop:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerStopIsIdempotent(t *testing.T) {
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", calls: &calls},
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("first stop failed: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("second stop failed: %v", err)
	}

	want := []string{"start:one", "start:two", "stop:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerStopAfterFailedStartDoesNotDoubleStop(t *testing.T) {
	startErr := errors.New("start failed")
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", startErr: startErr, calls: &calls},
	)

	err := manager.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("start err = %v, want %v", err, startErr)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("stop after failed start failed: %v", err)
	}

	want := []string{"start:one", "start:two", "stop:one"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestManagerStopPassesContextToComponents(t *testing.T) {
	var calls []string
	manager := NewManager(
		&fakeComponent{name: "one", calls: &calls},
		&fakeComponent{name: "two", calls: &calls},
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	ctx := context.WithValue(context.Background(), contextKey("marker"), "caller-context")
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	want := []string{"start:one", "start:two", "stop:two:caller-context", "stop:one:caller-context"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

type joinedErrors interface {
	Unwrap() []error
}

func assertJoinedErrors(t *testing.T, err error, want []error) {
	t.Helper()
	joined, ok := err.(joinedErrors)
	if !ok {
		t.Fatalf("err %T does not expose joined errors", err)
	}
	got := joined.Unwrap()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("joined errors = %v, want %v", got, want)
	}
}
