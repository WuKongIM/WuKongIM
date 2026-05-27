package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestStartOrderIsClusterThenGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start", got)
	}
}

func TestGatewayStartFailureStopsCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop", got)
	}
}

func TestStopOrderIsGatewayThenCluster(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,gateway.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,gateway.stop,cluster.stop", got)
	}
}

func TestConcurrentStartStopCannotLeaveGatewayRunningAfterStopReturns(t *testing.T) {
	cluster := newBlockingCluster()
	gateway := newStateGateway()
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- app.Start(context.Background())
	}()
	<-cluster.startEntered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- app.Stop(context.Background())
	}()
	time.Sleep(10 * time.Millisecond)

	close(cluster.releaseStart)

	if err := <-startDone; err != nil && !errors.Is(err, ErrStopped) {
		t.Fatalf("Start() error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if gateway.runningState() {
		t.Fatalf("gateway is running after Stop returned")
	}
}

func TestRollbackStopFailureLeavesClusterCleanupRetryPossible(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	rollbackErr := errors.New("cluster rollback failed")
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls, stopErr: rollbackErr}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("Start() error = %v, want gateway and rollback errors", err)
	}
	if err := app.Stop(context.Background()); !errors.Is(err, rollbackErr) {
		t.Fatalf("Stop() error = %v, want rollback retry error", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop,cluster.stop", got)
	}
}

func TestNewSeedsMessageIDsFromEffectiveClusterNodeID(t *testing.T) {
	app, err := New(Config{Cluster: clusterv2.Config{NodeID: 7}}, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ids := messageIDAllocatorFromApp(t, app)
	if got, want := ids.Next(), uint64(7<<48)+1; got != want {
		t.Fatalf("first message id = %d, want %d", got, want)
	}
}

type fakeCluster struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeCluster) Start(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.start")
	}
	return f.startErr
}

func (f *fakeCluster) Stop(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.stop")
	}
	return f.stopErr
}

type fakeGateway struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeGateway) Start() error {
	*f.calls = append(*f.calls, "gateway.start")
	return f.startErr
}

func (f *fakeGateway) Stop() error {
	*f.calls = append(*f.calls, "gateway.stop")
	return f.stopErr
}

func joinCalls(calls []string) string {
	return strings.Join(calls, ",")
}

type blockingCluster struct {
	startEntered chan struct{}
	releaseStart chan struct{}
}

func newBlockingCluster() *blockingCluster {
	return &blockingCluster{
		startEntered: make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
}

func (f *blockingCluster) Start(context.Context) error {
	close(f.startEntered)
	<-f.releaseStart
	return nil
}

func (f *blockingCluster) Stop(context.Context) error {
	return nil
}

type stateGateway struct {
	mu      sync.Mutex
	running bool
}

func newStateGateway() *stateGateway {
	return &stateGateway{}
}

func (f *stateGateway) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	return nil
}

func (f *stateGateway) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}

func (f *stateGateway) runningState() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

type messageIDAllocator interface {
	Next() uint64
}

func messageIDAllocatorFromApp(t *testing.T, app *App) messageIDAllocator {
	t.Helper()
	messages := app.Messages()
	if messages == nil {
		t.Fatalf("Messages() = nil")
	}
	field := reflect.ValueOf(messages).Elem().FieldByName("messageID")
	if !field.IsValid() {
		t.Fatalf("message app has no messageID field")
	}
	ids, ok := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(messageIDAllocator)
	if !ok {
		t.Fatalf("messageID field does not implement Next() uint64")
	}
	return ids
}
