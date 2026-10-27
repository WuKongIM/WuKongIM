//go:build integration

package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/trace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These regressions use the coordinator's actual WorkerClient.Checkpoint POST.
// The positive case creates native harness evidence through the real factory,
// Engine and worker ticker. Its semantic fake clock is not a physical-stall
// reproduction, and the optional child capture is not a CLI-owner E2E test.
type checkpointRuntimeFixture struct {
	server          *WorkerServer
	client          *WorkerClient
	httpClient      *http.Client
	url             string
	fence           WorkerFence
	started         bool
	notifications   atomic.Uint32
	lockHeld        atomic.Bool
	requestMu       sync.Mutex
	requestCount    int
	requestInvalid  bool
	lastRequest     WorkerCheckpointRequest
	handlerReturned chan struct{}
}

func checkpointRuntimeConfig() Config {
	cfg := LocalConfig()
	cfg.RunID = "worker-runtime-checkpoint"
	cfg.Workload.OnlineUsers = cfg.Workload.Workers
	return cfg
}

func newCheckpointRuntimeFixture(t *testing.T, factory WorkerGenerationFactory, enabled bool, notify func()) *checkpointRuntimeFixture {
	t.Helper()
	f := &checkpointRuntimeFixture{
		fence:           WorkerFence{RunID: checkpointRuntimeConfig().RunID, AssignmentID: "checkpoint-assignment", Generation: 7},
		handlerReturned: make(chan struct{}, 16),
	}
	cfg := WorkerServerConfig{ControlToken: "control-secret", Factory: factory}
	if enabled {
		cfg.ReportHarnessFailure = func() {
			if !f.server.mu.TryLock() {
				f.lockHeld.Store(true)
			} else {
				f.server.mu.Unlock()
			}
			f.notifications.Add(1)
			if notify != nil {
				notify()
			}
		}
	}
	var err error
	f.server, err = NewWorkerServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat-lifecycle/checkpoint" {
			defer func() {
				select {
				case f.handlerReturned <- struct{}{}:
				default:
				}
			}()
			raw, readErr := io.ReadAll(io.LimitReader(r.Body, workerMaxRequestBytes+1))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var request WorkerCheckpointRequest
			var fields map[string]json.RawMessage
			invalid := readErr != nil || int64(len(raw)) > workerMaxRequestBytes || r.Method != http.MethodPost ||
				json.Unmarshal(raw, &request) != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 3 ||
				fields["run_id"] == nil || fields["assignment_id"] == nil || fields["generation"] == nil
			f.requestMu.Lock()
			f.requestCount++
			f.requestInvalid = f.requestInvalid || invalid
			f.lastRequest = request
			f.requestMu.Unlock()
		}
		f.server.ServeHTTP(w, r)
	}))
	f.url, f.httpClient = httpServer.URL, httpServer.Client()
	f.httpClient.Timeout = 2 * time.Second
	f.client = f.workerClient(t, "control-secret")
	t.Cleanup(func() {
		if f.started {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, stopErr := f.client.Stop(ctx, WorkerStopRequest{WorkerFence: f.fence})
			cancel()
			if stopErr != nil {
				f.server.mu.Lock()
				generation := f.server.generation
				f.server.mu.Unlock()
				if generation != nil {
					generation.Stop()
				}
				t.Errorf("checkpoint fixture Stop failed: %T", stopErr)
			}
		}
		f.httpClient.CloseIdleConnections()
		httpServer.Close()
	})
	return f
}

func (f *checkpointRuntimeFixture) workerClient(t *testing.T, token string) *WorkerClient {
	t.Helper()
	client, err := NewWorkerClient(WorkerClientConfig{BaseURL: f.url, ControlToken: token, HTTPClient: f.httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func (f *checkpointRuntimeFixture) assignStart(t *testing.T) {
	t.Helper()
	cfg := checkpointRuntimeConfig()
	_, err := f.client.Assign(context.Background(), WorkerAssignment{
		WorkerFence: f.fence, WorkerID: 0, WorkerCount: uint64(cfg.Workload.Workers), Config: cfg, CoordinatorGrants: true,
	})
	if err != nil {
		t.Fatalf("real HTTP Assign: %T", err)
	}
	if _, err := f.client.Start(context.Background(), WorkerStartRequest{WorkerFence: f.fence}); err != nil {
		t.Fatalf("real HTTP Start: %T", err)
	}
	f.started = true
}

func (f *checkpointRuntimeFixture) assertRequest(t *testing.T, count int) {
	t.Helper()
	f.requestMu.Lock()
	defer f.requestMu.Unlock()
	if f.requestInvalid || f.requestCount != count || f.lastRequest.WorkerFence != f.fence {
		t.Fatal("Checkpoint did not preserve the real POST path and exact three fence fields")
	}
}

func (f *checkpointRuntimeFixture) awaitHandler(t *testing.T) {
	t.Helper()
	select {
	case <-f.handlerReturned:
	case <-time.After(time.Second):
		t.Fatal("real checkpoint HTTP handler did not join")
	}
}

// The existing factory composes all real engine components. Only its existing
// clock, SDK and sync ports are fixtures; no private harness counter is seeded.
func newCheckpointRuntimeEngine(t *testing.T, enabled bool, notify func()) *checkpointRuntimeFixture {
	t.Helper()
	at := time.Unix(1_700_000_000, 0)
	clock := &sessionFakeClock{now: at}
	sessions := &engineFakeFactory{autoAck: true}
	ticker := newManualWorkerGenerationTicker()
	factory := engineWorkerGenerationFactory{
		clock: clock,
		newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) {
			return driverTestFactory{base: sessions, onSend: func() { clock.Set(at.Add(101 * time.Millisecond)) }}, nil
		},
		newSyncer: func(WorkerAssignment) (ConversationSyncer, error) { return engineSyncer{}, nil },
		newTicker: func(time.Duration) workerGenerationTicker { return ticker },
	}
	f := newCheckpointRuntimeFixture(t, factory, enabled, notify)
	f.assignStart(t)
	ticker.awaitReady(t)
	f.server.mu.Lock()
	generation, ok := f.server.generation.(*engineWorkerGeneration)
	f.server.mu.Unlock()
	if !ok {
		t.Fatal("HTTP assignment did not construct the real engine generation")
	}
	uid := generation.engine.sessions.identity.UID(0)
	if _, err := generation.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0, LoginOrdinal: 0}); err != nil {
		t.Fatal(err)
	}
	view := engineTestFixture{engine: generation.engine, pool: generation.engine.sessions, traffic: generation.engine.traffic}
	driverQueueSends(t, view, uid, at, 2)
	ticker.tickAndWait(t)
	waitForEngineCompletions(t, generation.engine, "checkpoint real-engine SEND")
	if sessions.sentCount() != 1 {
		t.Fatal("real driver did not retain exactly one accepted SDK SEND before code38")
	}
	select {
	case <-generation.Done():
		t.Fatal("evidence-only worker tick unexpectedly terminated generation")
	default:
	}
	return f
}

func assertCheckpointRuntimeNative(t *testing.T, snapshot WorkerSnapshot) {
	t.Helper()
	if snapshot.Harness.Failures != 1 || snapshot.Harness.OfferedUnderdelivery != 1 || snapshot.Harness.Classification != SyncClassificationHarnessInvalid ||
		!workerEvidenceHasCode(snapshot.Evidence, FailureCode(38)) {
		t.Fatal("real engine checkpoint did not preserve native code38 harness evidence")
	}
	if snapshot.Messages.Sent != 1 || snapshot.Messages.SendAcknowledged != 1 || snapshot.Messages.FirstAttempts != 1 ||
		snapshot.Messages.FirstAttemptTimeouts != 0 || snapshot.Messages.FirstAttemptFailures != 0 || snapshot.Messages.Terminal != 0 ||
		snapshot.Messages.Losses != 0 || snapshot.Messages.Duplicates != 0 || snapshot.Messages.Corruptions != 0 || snapshot.Messages.SequenceRegressions != 0 {
		t.Fatal("checkpoint notification changed native message/correctness counters")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || bytes.Contains(encoded, []byte("runtime_trace")) {
		t.Fatal("private capture entered native worker wire")
	}
	t.Logf("real_engine_checkpoint code38=true harness_failures=1 offered_underdelivery=1 sent=1 ack=1 correctness=0 physical_historical_cause=unknown")
}

func TestWorkerRuntimeTraceCheckpointRealEngineEvidenceAndNilDefault(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "nil_default"
		if enabled {
			name = "notify"
		}
		t.Run(name, func(t *testing.T) {
			f := newCheckpointRuntimeEngine(t, enabled, nil)
			info, err := f.client.Info(context.Background())
			if err != nil || info.ProtocolVersion != 15 {
				t.Fatal("native worker protocol15 changed")
			}
			var prior WorkerSnapshot
			for cut := uint64(1); cut <= 2; cut++ {
				snapshot, err := f.client.Checkpoint(context.Background(), WorkerCheckpointRequest{WorkerFence: f.fence})
				if err != nil {
					t.Fatalf("real WorkerClient.Checkpoint: %T", err)
				}
				f.awaitHandler(t)
				assertCheckpointRuntimeNative(t, snapshot)
				if snapshot.SnapshotSequence != cut || snapshot.Phase != WorkerPhaseRunning || snapshot.Generation != f.fence.Generation {
					t.Fatal("checkpoint fence, phase or sequence changed")
				}
				stable := snapshot
				stable.SnapshotSequence, stable.Uptime = 0, 0
				if cut > 1 && !reflect.DeepEqual(prior, stable) {
					t.Fatal("repeated checkpoint changed native evidence")
				}
				prior = stable
			}
			f.assertRequest(t, 2)
			want := uint32(0)
			if enabled {
				want = 2
			}
			if f.notifications.Load() != want || f.lockHeld.Load() {
				t.Fatalf("checkpoint notifications=%d want=%d lifecycle_lock_held=%t", f.notifications.Load(), want, f.lockHeld.Load())
			}
		})
	}
}

type checkpointRuntimeResultGeneration struct {
	*fakeWorkerGeneration
	checkpointErr   error
	canceledEntered chan struct{}
}

func (g *checkpointRuntimeResultGeneration) Checkpoint(ctx context.Context) (WorkerSnapshot, error) {
	if g.canceledEntered != nil {
		close(g.canceledEntered)
		<-ctx.Done()
	}
	if g.checkpointErr != nil {
		return WorkerSnapshot{}, g.checkpointErr
	}
	return g.fakeWorkerGeneration.Checkpoint(ctx)
}

func TestWorkerRuntimeTraceCheckpointRejectsNonSuccessfulAndHealthyCuts(t *testing.T) {
	for _, mode := range []string{"unauthenticated", "wrong_fence", "runtime_error", "invalid_shape", "healthy", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			g := &checkpointRuntimeResultGeneration{fakeWorkerGeneration: newFakeWorkerGeneration()}
			g.snapshot = r28HarnessSnapshot(38)
			if mode == "runtime_error" {
				g.checkpointErr = errors.New("controlled checkpoint error")
			}
			if mode == "invalid_shape" {
				g.snapshot.Messages.FirstAttemptFailures = 1
			}
			if mode == "healthy" {
				g.snapshot = WorkerSnapshot{}
			}
			if mode == "canceled" {
				g.canceledEntered = make(chan struct{})
			}
			f := newCheckpointRuntimeFixture(t, WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return g, nil }), true, nil)
			f.assignStart(t)
			request := WorkerCheckpointRequest{WorkerFence: f.fence}
			client := f.client
			if mode == "unauthenticated" {
				client = f.workerClient(t, "wrong-token")
			}
			if mode == "wrong_fence" {
				request.Generation++
			}
			var err error
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { _, callErr := client.Checkpoint(ctx, request); done <- callErr }()
				select {
				case <-g.canceledEntered:
				case <-time.After(time.Second):
					t.Fatal("real canceled checkpoint never entered generation")
				}
				cancel()
				select {
				case err = <-done:
				case <-time.After(time.Second):
					t.Fatal("canceled checkpoint client did not join")
				}
				f.awaitHandler(t)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled checkpoint error=%T", err)
				}
			} else {
				_, err = client.Checkpoint(context.Background(), request)
				f.awaitHandler(t)
				want := map[string]int{"unauthenticated": 401, "wrong_fence": 409, "runtime_error": 422, "invalid_shape": 500, "healthy": 200}[mode]
				if want == 200 {
					if err != nil {
						t.Fatal("healthy checkpoint failed")
					}
				} else {
					var api *WorkerAPIError
					if !errors.As(err, &api) || api.Status != want {
						t.Fatalf("checkpoint error=%T want HTTP%d", err, want)
					}
				}
			}
			if f.notifications.Load() != 0 || f.lockHeld.Load() {
				t.Fatal("unsuccessful, canceled or healthy checkpoint notified")
			}
		})
	}
}

func TestWorkerRuntimeTraceCheckpointOldGenerationCannotNotify(t *testing.T) {
	old := newBlockingControlGeneration("checkpoint")
	old.snapshot = r28HarnessSnapshot(38)
	fresh := newFakeWorkerGeneration()
	var created atomic.Uint32
	f := newCheckpointRuntimeFixture(t, WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
		if created.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	}), true, nil)
	f.assignStart(t)
	var release sync.Once
	unblock := func() { release.Do(func() { close(old.controlRelease) }) }
	defer unblock()
	done := make(chan error, 1)
	request := WorkerCheckpointRequest{WorkerFence: f.fence}
	go func() { _, err := f.client.Checkpoint(context.Background(), request); done <- err }()
	select {
	case <-old.controlStarted:
	case <-time.After(time.Second):
		t.Fatal("old checkpoint never entered generation")
	}
	if _, err := f.client.Stop(context.Background(), WorkerStopRequest{WorkerFence: f.fence}); err != nil {
		t.Fatal(err)
	}
	f.started = false
	f.fence.AssignmentID, f.fence.Generation = "checkpoint-next", 8
	f.assignStart(t)
	unblock()
	select {
	case err := <-done:
		var api *WorkerAPIError
		if !errors.As(err, &api) || api.Status != 409 || api.Code != WorkerErrorFenceMismatch {
			t.Fatal("stale checkpoint was not rejected at the existing fence")
		}
	case <-time.After(time.Second):
		t.Fatal("old checkpoint did not join")
	}
	f.awaitHandler(t)
	if f.notifications.Load() != 0 {
		t.Fatal("old generation checkpoint notified as current")
	}
	if _, err := f.client.Checkpoint(context.Background(), WorkerCheckpointRequest{WorkerFence: f.fence}); err != nil {
		t.Fatal(err)
	}
	f.awaitHandler(t)
	if f.notifications.Load() != 0 {
		t.Fatal("new healthy generation inherited old failure notification")
	}
}

// This child verifies producer callback -> actual runtime stream only. The
// separate CLI-owner real-process tests own asynchronous export/Stop behavior.
func TestWorkerRuntimeTraceCheckpointChild(t *testing.T) {
	if os.Getenv("WK_CHECKPOINT_TRACE_CHILD") != "1" {
		return
	}
	output := os.Getenv("WK_CHECKPOINT_TRACE_OUTPUT")
	if !filepath.IsAbs(output) {
		t.Fatal("checkpoint child output must be absolute")
	}
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{MaxBytes: 8 << 20, MinAge: 10 * time.Second})
	if err := recorder.Start(); err != nil {
		t.Fatal(err)
	}
	defer recorder.Stop()
	requested := make(chan struct{}, 1)
	var once sync.Once
	f := newCheckpointRuntimeEngine(t, true, func() {
		once.Do(func() {
			select {
			case requested <- struct{}{}:
			default:
			}
		})
	})
	for i := 0; i < 3; i++ {
		snapshot, err := f.client.Checkpoint(context.Background(), WorkerCheckpointRequest{WorkerFence: f.fence})
		if err != nil {
			t.Fatal(err)
		}
		f.awaitHandler(t)
		assertCheckpointRuntimeNative(t, snapshot)
	}
	f.assertRequest(t, 3)
	select {
	case <-requested:
	default:
		t.Fatal("real Checkpoint callback did not request runtime capture")
	}
	if f.notifications.Load() != 3 || f.lockHeld.Load() {
		t.Fatal("checkpoint callback selection or lifecycle mutex changed")
	}
	select {
	case <-requested:
		t.Fatal("producer adapter requested more than one test capture")
	default:
	}
	file, err := os.OpenFile(output+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, exportErr := recorder.WriteTo(&r28RegionWriter{target: file, remaining: 16 << 20})
	closeErr := file.Close()
	recorder.Stop()
	if exportErr != nil || closeErr != nil {
		t.Fatalf("checkpoint producer stream failed: %T/%T", exportErr, closeErr)
	}
	if err := os.Rename(output+".partial", output); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRuntimeTraceCheckpointProducesDecodableCallbackCapture(t *testing.T) {
	root := os.Getenv("WK_R28_TRACE_TEST_ARTIFACT_ROOT")
	if root == "" {
		root = t.TempDir()
	} else {
		if !filepath.IsAbs(root) {
			t.Fatal("artifact root must be absolute")
		}
		root = filepath.Join(root, "checkpoint-producer")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "trace.out")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestWorkerRuntimeTraceCheckpointChild$", "-test.timeout=6s")
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "WK_") {
			child.Env = append(child.Env, pair)
		}
	}
	child.Env = append(child.Env, "WK_CHECKPOINT_TRACE_CHILD=1", "WK_CHECKPOINT_TRACE_OUTPUT="+output)
	var childOutput r28DecodeBuffer
	child.Stdout, child.Stderr = &childOutput, &childOutput
	if err := child.Run(); err != nil || childOutput.truncated {
		t.Fatalf("checkpoint child failed/truncated: %T output=%s", err, childOutput.String())
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 16<<20 {
		t.Fatal("checkpoint runtime stream identity/size/permission invalid")
	}
	decodeCtx, decodeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer decodeCancel()
	decoder := exec.CommandContext(decodeCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "tool", "trace", "-d=parsed", output)
	var decoded r28DecodeBuffer
	decoder.Stdout, decoder.Stderr = &decoded, &decoded
	if err := decoder.Run(); err != nil || decoded.truncated || !bytes.Contains(decoded.Bytes(), []byte("StateTransition")) {
		t.Fatalf("actual checkpoint trace decode failed/truncated: %T", err)
	}
	if err := os.WriteFile(filepath.Join(root, "decoded.txt"), decoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("checkpoint_producer_actual_runtime_stream bytes=%d coverage=unknown CLI_owner_composition=separate physical_historical_cause=unknown", info.Size())
}
