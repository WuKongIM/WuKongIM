//go:build integration

package chatlifecycle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the production generation's default HTTP syncer, not an injected
// bounded double. Real CONNECT is replaced by the existing session port only.
func TestWorkerBootstrapFullSyncBoundsHTTPWithoutSerializingConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var active, peak, requests, refused atomic.Int64
	admitted := make(chan struct{}, 100)
	var addrs []string
	for i := 0; i < 3; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			if n > 16 {
				refused.Add(1)
				http.Error(w, `{"code":"unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			admitted <- struct{}{}
			select {
			case <-release:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"conversations":[],"deletes":[],"next_cursor":"","done":true,"coverage":1}`))
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(server.Close)
		addrs = append(addrs, server.URL)
	}
	config := LocalConfig()
	config.Observation.APIAddrs = addrs
	connected := make(chan context.Context, 100)
	connectRelease := make(chan struct{})
	close(connectRelease)
	var pools []*SessionPool
	var work sync.WaitGroup
	defer func() {
		unblock()
		cancel()
		work.Wait()
		for _, pool := range pools {
			if err := pool.CloseAll(); err != nil {
				t.Errorf("join sessions: %v", err)
			}
		}
	}()
	for workerID := uint64(0); workerID < 3; workerID++ {
		factory := NewEngineWorkerGenerationFactory("test-token").(engineWorkerGenerationFactory)
		factory.newSessionFactory = func(WorkerAssignment) (SessionClientFactory, error) {
			return &engineFakeFactory{connectStarted: connected, connectRelease: connectRelease}, nil
		}
		generation, err := factory.New(WorkerAssignment{
			WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: fmt.Sprintf("sync-admission-%d", workerID), Generation: 1},
			WorkerID:    workerID, WorkerCount: 3, Config: config,
		})
		if err != nil {
			t.Fatal(err)
		}
		pools = append(pools, generation.(*engineWorkerGeneration).engine.sessions)
	}
	results := make(chan error, 100)
	for index := uint64(0); index < 100; index++ {
		pool := pools[index%3]
		work.Add(1)
		go func() {
			defer work.Done()
			snapshot, err := pool.Login(ctx, SessionLogin{UID: pool.identity.UID(index), UserIndex: index, LoginOrdinal: index / 3})
			if err == nil && !snapshot.TrafficReady {
				err = fmt.Errorf("login completed without sync readiness")
			}
			results <- err
		}()
	}
	for i := 0; i < 100; i++ {
		select {
		case <-connected:
		case <-ctx.Done():
			t.Fatal("full-sync backpressure serialized or lost CONNECT")
		}
	}
	for i := 0; i < 12; i++ {
		select {
		case <-admitted:
		case <-ctx.Done():
			t.Fatal("production full-sync client did not use bounded parallelism")
		}
	}
	// Keep the original one-second bootstrap cohort concurrent long enough for
	// excess HTTP calls to reach the explicit refusal boundary. This is fixture
	// scheduling allowance, not a product latency assertion or retry interval.
	timer := time.NewTimer(250 * time.Millisecond)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
		t.Fatal(ctx.Err())
	}
	unblock()
	failed := 0
	for i := 0; i < 100; i++ {
		if err := <-results; err != nil {
			failed++
		}
	}
	if failed != 0 || refused.Load() != 0 || peak.Load() > 12 || requests.Load() != 100 {
		t.Fatalf("bootstrap sync failures=%d HTTP refusals=%d peak=%d requests=%d; want0/0/<=12/100 without retry", failed, refused.Load(), peak.Load(), requests.Load())
	}
	t.Logf("100 CONNECT and full-sync completions; HTTP peak=%d, requests=%d, refusals=0; original readiness and failure accounting retained", peak.Load(), requests.Load())
}

func TestWorkerFullSyncQueuedCancellationIncludesWaitAndNeverDispatches(t *testing.T) {
	fixture := newWorkerSyncHTTPFixture(t)
	syncer := productionWorkerSyncer(t, fixture.addrs, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := holdWorkerSyncRequests(t, ctx, syncer, fixture)
	queued, cancelQueued := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelQueued()
	// The canceled queued call has no HTTP timing source. Supply independent
	// cuts so the assertion does not depend on timer/scheduler overshoot.
	anchor := time.Unix(1700000000, 0)
	cuts := 0
	now := func() time.Time {
		cuts++
		if cuts == 4 {
			return anchor.Add(700 * time.Millisecond)
		}
		return anchor
	}
	result, err := RunLoginSync(queued, "queued-cancel", workerSyncImmediateConnector{}, syncer, now)
	failure, ok := LoginSyncFailureOf(err)
	if !ok || failure.Stage != LoginSyncStageSync || failure.Reason != LoginSyncReasonCanceled || result.TrafficReady || !result.SyncStarted || !result.ConnectCompleted {
		t.Fatalf("queued cancellation result=%+v failure=%+v error=%v", result, failure, err)
	}
	if result.ConversationSyncLatency != 700*time.Millisecond || fixture.requests.Load() != 4 {
		t.Fatalf("queue wait excluded or canceled request dispatched: latency=%v HTTP=%d", result.ConversationSyncLatency, fixture.requests.Load())
	}
	fixture.unblock()
	joinWorkerSyncRequests(t, results, false)
	result, err = RunLoginSync(ctx, "after-queued-cancel", workerSyncImmediateConnector{}, syncer, time.Now)
	if err != nil || !result.TrafficReady || fixture.requests.Load() != 5 {
		t.Fatalf("permit lost or canceled waiter later dispatched: result=%+v HTTP=%d error=%v", result, fixture.requests.Load(), err)
	}
}

func TestWorkerFullSyncActiveCancellationReleasesAdmission(t *testing.T) {
	fixture := newWorkerSyncHTTPFixture(t)
	syncer := productionWorkerSyncer(t, fixture.addrs, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := holdWorkerSyncRequests(t, ctx, syncer, fixture)
	cancel()
	joinWorkerSyncRequests(t, results, true)
	fixture.unblock()
	fresh, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	result, err := RunLoginSync(fresh, "after-active-cancel", workerSyncImmediateConnector{}, syncer, time.Now)
	if err != nil || !result.TrafficReady {
		t.Fatalf("active cancellation leaked admission: result=%+v error=%v", result, err)
	}
}

func TestWorkerFullSyncPreservesHTTPFailureAndReleasesAdmission(t *testing.T) {
	fixture := newWorkerSyncHTTPFixture(t)
	fixture.unavailable.Store(true)
	fixture.unblock()
	syncer := productionWorkerSyncer(t, fixture.addrs, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := RunLoginSync(ctx, "refused", workerSyncImmediateConnector{}, syncer, time.Now)
	failure, ok := LoginSyncFailureOf(err)
	if !ok || failure.Stage != LoginSyncStageSync || failure.Reason != LoginSyncReasonTransport || result.TrafficReady || fixture.requests.Load() != 3 {
		t.Fatalf("HTTP failure hidden or new retry added: failure=%+v result=%+v requests=%d error=%v", failure, result, fixture.requests.Load(), err)
	}
	fixture.unavailable.Store(false)
	result, err = RunLoginSync(ctx, "after-refusal", workerSyncImmediateConnector{}, syncer, time.Now)
	if err != nil || !result.TrafficReady || fixture.requests.Load() != 4 {
		t.Fatalf("failed sync leaked admission: result=%+v HTTP=%d error=%v", result, fixture.requests.Load(), err)
	}
}

func TestWorkerFullSyncAdmissionBelongsToFreshGeneration(t *testing.T) {
	fixture := newWorkerSyncHTTPFixture(t)
	first := productionWorkerSyncer(t, fixture.addrs, 1)
	second := productionWorkerSyncer(t, fixture.addrs, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := holdWorkerSyncRequests(t, ctx, first, fixture)
	secondDone := make(chan error, 1)
	var work sync.WaitGroup
	defer func() {
		fixture.unblock()
		cancel()
		work.Wait()
	}()
	work.Add(1)
	go func() {
		defer work.Done()
		_, err := RunLoginSync(ctx, "fresh-generation", workerSyncImmediateConnector{}, second, time.Now)
		secondDone <- err
	}()
	select {
	case <-fixture.admitted:
	case <-ctx.Done():
		t.Fatal("old generation's admission blocked a fresh generation")
	}
	fixture.unblock()
	joinWorkerSyncRequests(t, results, false)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

type workerSyncHTTPFixture struct {
	addrs       []string
	admitted    chan struct{}
	requests    atomic.Int64
	unavailable atomic.Bool
	release     chan struct{}
	once        sync.Once
}

func newWorkerSyncHTTPFixture(t *testing.T) *workerSyncHTTPFixture {
	t.Helper()
	f := &workerSyncHTTPFixture{admitted: make(chan struct{}, 100), release: make(chan struct{})}
	for i := 0; i < 3; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.requests.Add(1)
			if f.unavailable.Load() {
				http.Error(w, `{"code":"unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			f.admitted <- struct{}{}
			select {
			case <-f.release:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"conversations":[],"deletes":[],"next_cursor":"","done":true,"coverage":1}`))
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(server.Close)
		f.addrs = append(f.addrs, server.URL)
	}
	t.Cleanup(f.unblock)
	return f
}

func (f *workerSyncHTTPFixture) unblock() { f.once.Do(func() { close(f.release) }) }

func productionWorkerSyncer(t *testing.T, addrs []string, generation uint64) ConversationSyncer {
	t.Helper()
	config := LocalConfig()
	config.Observation.APIAddrs = addrs
	worker, err := NewEngineWorkerGenerationFactory("test-token").New(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "sync-failure-scenarios", Generation: generation},
		WorkerID:    0, WorkerCount: 3, Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker.(*engineWorkerGeneration).engine.sessions.syncer
}

func holdWorkerSyncRequests(t *testing.T, ctx context.Context, syncer ConversationSyncer, fixture *workerSyncHTTPFixture) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	var work sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		fixture.unblock()
		work.Wait()
	})
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			_, err := RunLoginSync(ctx, "held-sync", workerSyncImmediateConnector{}, syncer, time.Now)
			results <- err
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-fixture.admitted:
		case <-ctx.Done():
			t.Fatal("full-sync admission did not fill")
		}
	}
	return results
}

func joinWorkerSyncRequests(t *testing.T, results <-chan error, canceled bool) {
	t.Helper()
	for i := 0; i < 4; i++ {
		err := <-results
		if !canceled {
			if err != nil {
				t.Errorf("admitted sync failed: %v", err)
			}
			continue
		}
		failure, ok := LoginSyncFailureOf(err)
		if !ok || failure.Reason != LoginSyncReasonCanceled || failure.Stage != LoginSyncStageSync {
			t.Errorf("active canceled sync lost failure: %+v error=%v", failure, err)
		}
	}
}

type workerSyncImmediateConnector struct{}

func (workerSyncImmediateConnector) Connect(context.Context, string) error { return nil }
