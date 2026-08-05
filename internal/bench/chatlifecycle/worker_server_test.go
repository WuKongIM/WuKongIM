package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkerServerRequiresBearerAuthenticationOnEveryEndpoint(t *testing.T) {
	t.Parallel()

	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/healthz", ""},
		{http.MethodGet, "/v1/info", ""},
		{http.MethodPost, "/v1/chat-lifecycle/assign", `{}`},
		{http.MethodPost, "/v1/chat-lifecycle/start", `{}`},
		{http.MethodGet, "/v1/chat-lifecycle/status", ""},
		{http.MethodGet, "/v1/chat-lifecycle/snapshot", ""},
		{http.MethodPost, "/v1/chat-lifecycle/checkpoint", `{}`},
		{http.MethodPost, "/v1/chat-lifecycle/rate", `{}`},
		{http.MethodPost, "/v1/chat-lifecycle/stop", `{}`},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			response := httptest.NewRecorder()

			server.ServeHTTP(response, req)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusUnauthorized, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("content type = %q, want application/json", got)
			}
			if got := response.Body.String(); got != `{"code":"unauthorized"}`+"\n" {
				t.Fatalf("body = %q", got)
			}
		})
	}
}

func TestWorkerServerUsesStableErrorsForUnknownRoutesAndMethods(t *testing.T) {
	t.Parallel()

	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return newFakeWorkerGeneration(), nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}
	assertWorkerError(t, server, http.MethodPost, "/healthz", nil, http.StatusMethodNotAllowed, WorkerErrorMethodNotAllowed)
	assertWorkerError(t, server, http.MethodGet, "/v1/chat-lifecycle/missing", nil, http.StatusNotFound, WorkerErrorNotFound)
}

func TestWorkerServerStrictlyDecodesBoundedMutationBodies(t *testing.T) {
	t.Parallel()

	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return newFakeWorkerGeneration(), nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}

	unknown := httptest.NewRequest(http.MethodPost, "/v1/chat-lifecycle/start", strings.NewReader(`{"run_id":"run","assignment_id":"a","generation":1,"raw_uid":"forbidden"}`))
	unknown.Header.Set("Authorization", "Bearer control-secret")
	unknownResponse := httptest.NewRecorder()
	server.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest || unknownResponse.Body.String() != `{"code":"invalid_json"}`+"\n" {
		t.Fatalf("unknown-field response = %d/%q", unknownResponse.Code, unknownResponse.Body.String())
	}

	oversizedBody := `{"run_id":"` + strings.Repeat("x", int(workerMaxRequestBytes))
	oversized := httptest.NewRequest(http.MethodPost, "/v1/chat-lifecycle/start", strings.NewReader(oversizedBody))
	oversized.Header.Set("Authorization", "Bearer control-secret")
	oversizedResponse := httptest.NewRecorder()
	server.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge || oversizedResponse.Body.String() != `{"code":"request_too_large"}`+"\n" {
		t.Fatalf("oversized response = %d/%q", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestWorkerServerDrainTimeoutProducesClosedHarnessFinalState(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
	generation.drainErr = context.DeadlineExceeded
	server, fence := startFakeWorkerServer(t, generation, "timeout")

	response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
	if response.Code != http.StatusOK {
		t.Fatalf("stop status = %d; body = %q", response.Code, response.Body.String())
	}
	var snapshot WorkerSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode stop snapshot: %v", err)
	}
	if snapshot.Phase != WorkerPhaseFinal || !snapshot.Harness.DrainTimedOut || snapshot.Harness.Classification != SyncClassificationHarnessInvalid || snapshot.Harness.Failures == 0 {
		t.Fatalf("timeout final snapshot = %+v", snapshot)
	}
	if generation.drains != 1 || generation.stops != 1 {
		t.Fatalf("drain/stop calls = %d/%d, want 1/1", generation.drains, generation.stops)
	}
}

func TestWorkerServerUnexpectedGenerationExitPublishesRedactedFinalSignal(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
	server, _ := startFakeWorkerServer(t, generation, "unexpected")
	generation.terminate(errors.New("secret token raw_uid channel_id"))
	<-server.UnexpectedExit()

	response := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d; body = %q", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"secret", "raw_uid", "channel_id"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("snapshot contains %q: %q", forbidden, response.Body.String())
		}
	}
	var snapshot WorkerSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Phase != WorkerPhaseFinal || !snapshot.Harness.UnexpectedExit || snapshot.Harness.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("unexpected final snapshot = %+v", snapshot)
	}
}

func TestWorkerEngineGenerationFactoryComposesExistingEngineWithoutIO(t *testing.T) {
	t.Parallel()

	config := LocalConfig()
	generation, err := NewEngineWorkerGenerationFactory().New(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "real-engine", Generation: 1},
		WorkerID:    1, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	if err != nil {
		t.Fatalf("new engine generation: %v", err)
	}
	engineGeneration, ok := generation.(*engineWorkerGeneration)
	if !ok || engineGeneration.engine == nil || engineGeneration.verifier == nil {
		t.Fatalf("generation does not own existing engine/verifier: %#v", generation)
	}
	snapshot := generation.Snapshot()
	if snapshot.WorkerID != 1 || snapshot.WorkerCount != 3 || snapshot.Sessions.Target != 33 {
		t.Fatalf("pre-start engine snapshot = %+v", snapshot)
	}
	if err := generation.UpdateRate(100, 200); !errors.Is(err, errEngineNotRunning) {
		t.Fatalf("pre-start rate error = %v, want %v", err, errEngineNotRunning)
	}
	engineGeneration.engine.lifecycleMu.Lock()
	engineGeneration.engine.cached.GatewayConnectLatency = newWorkerHistogramSnapshot()
	recordWorkerLatency(&engineGeneration.engine.cached.GatewayConnectLatency, 20*time.Millisecond)
	engineGeneration.engine.cached.ConversationSyncLatency = newWorkerHistogramSnapshot()
	recordWorkerLatency(&engineGeneration.engine.cached.ConversationSyncLatency, 50*time.Millisecond)
	engineGeneration.engine.lifecycleMu.Unlock()
	engineGeneration.verifier.sendMu.Lock()
	recordWorkerLatency(&engineGeneration.verifier.sendackLatency, 2*time.Second)
	engineGeneration.verifier.sendMu.Unlock()
	engineGeneration.verifier.recvMu.Lock()
	recordWorkerLatency(&engineGeneration.verifier.recvackLatency, 50*time.Millisecond)
	engineGeneration.verifier.recvMu.Unlock()
	snapshot = generation.Snapshot()
	if snapshot.Sync.ConnectLatency.Buckets[5] != 1 || snapshot.Sync.Latency.Buckets[6] != 1 ||
		snapshot.SendackLatency.Buckets[11] != 1 || snapshot.RecvackLatency.Buckets[6] != 1 {
		t.Fatalf("worker latency projection = sync=%+v sendack=%+v recvack=%+v", snapshot.Sync, snapshot.SendackLatency, snapshot.RecvackLatency)
	}
}

func startFakeWorkerServer(t *testing.T, generation *fakeWorkerGeneration, assignmentID string) (*WorkerServer, WorkerFence) {
	t.Helper()
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}
	config := LocalConfig()
	fence := WorkerFence{RunID: config.RunID, AssignmentID: assignmentID, Generation: 1}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})
	return server, fence
}

func TestWorkerSnapshotSchemaIsIdentityFreeAndServerRejectsUnboundedEvidence(t *testing.T) {
	t.Parallel()

	assertIdentityFreeSnapshotType(t, reflect.TypeOf(WorkerSnapshot{}), map[reflect.Type]bool{})

	generation := newFakeWorkerGeneration()
	generation.snapshot.Evidence.Classes = []EvidenceClassSnapshot{{
		Class: FailureClassHarness,
		First: make([]EvidenceExample, maxEvidenceExamplesPerSide+1),
	}}
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}
	config := LocalConfig()
	fence := WorkerFence{RunID: config.RunID, AssignmentID: "bounded", Generation: 1}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})

	response := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot status = %d, want %d; body = %q", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	var apiError WorkerAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil || apiError.Code != WorkerErrorRuntimeFailure {
		t.Fatalf("snapshot API error = %+v, decode error = %v", apiError, err)
	}
}

func TestWorkerLatencyHistogramUsesFixedBucketsAndSaturates(t *testing.T) {
	t.Parallel()

	histogram := newWorkerHistogramSnapshot()
	recordWorkerLatency(&histogram, -time.Nanosecond)
	if histogram.Count != 0 {
		t.Fatalf("negative latency count = %d, want 0", histogram.Count)
	}
	recordWorkerLatency(&histogram, 0)
	if histogram.Count != 1 || histogram.Buckets[0] != 1 || histogram.MaxNanos != 0 {
		t.Fatalf("zero latency histogram = %+v", histogram)
	}

	const maximum = ^uint64(0)
	histogram.Count = maximum
	histogram.SumNanos = maximum - 1
	histogram.Buckets[15] = maximum
	recordWorkerLatency(&histogram, 61*time.Second)
	if histogram.Count != maximum || histogram.SumNanos != maximum || histogram.MaxNanos != uint64(61*time.Second) || histogram.Buckets[15] != maximum {
		t.Fatalf("saturated tail histogram = %+v", histogram)
	}
	if histogram.BucketUpper != workerLatencyBucketUpperNanos {
		t.Fatalf("bucket bounds = %v", histogram.BucketUpper)
	}
}

func assertIdentityFreeSnapshotType(t *testing.T, typ reflect.Type, visited map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Array || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || visited[typ] {
		return
	}
	visited[typ] = true
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		for _, forbidden := range []string{"uid", "user_id", "channel_id", "token", "secret"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("snapshot field %s.%s contains forbidden identity term %q", typ, field.Name, forbidden)
			}
		}
		assertIdentityFreeSnapshotType(t, field.Type, visited)
	}
}

func TestWorkerServerStopContinuesAfterRequestDisconnectAndCachesFinalSnapshot(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
	generation.drainStarted = make(chan struct{})
	generation.drainRelease = make(chan struct{})
	generation.stopped = make(chan struct{})
	now := time.Unix(1_700_000_000, 0)
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(assignment WorkerAssignment) (WorkerGeneration, error) {
			generation.snapshot.Messages.Sent = 41
			return generation, nil
		}),
		DrainTimeout: 20 * time.Second,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}
	fence := WorkerFence{RunID: "local-chat-lifecycle", AssignmentID: "disconnect", Generation: 1}
	config := LocalConfig()
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})

	encoded, err := json.Marshal(WorkerStopRequest{WorkerFence: fence})
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat-lifecycle/stop", strings.NewReader(string(encoded))).WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer control-secret")
	handlerDone := make(chan struct{})
	go func() {
		server.ServeHTTP(httptest.NewRecorder(), request)
		close(handlerDone)
	}()

	<-generation.drainStarted
	cancelRequest()
	<-handlerDone
	close(generation.drainRelease)
	<-generation.stopped

	response := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d; body = %q", response.Code, response.Body.String())
	}
	var snapshot WorkerSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Phase != WorkerPhaseFinal || snapshot.Messages.Sent != 41 {
		t.Fatalf("final snapshot = %+v", snapshot)
	}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
	if generation.drains != 1 || generation.stops != 1 {
		t.Fatalf("drain/stop calls = %d/%d, want 1/1", generation.drains, generation.stops)
	}
}

func TestWorkerServerEnforcesAssignmentGenerationAndLifecycle(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(assignment WorkerAssignment) (WorkerGeneration, error) {
			generation.snapshot.Generation = assignment.Generation
			generation.snapshot.WorkerID = assignment.WorkerID
			generation.snapshot.WorkerCount = assignment.WorkerCount
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}

	fence := WorkerFence{RunID: "local-chat-lifecycle", AssignmentID: "assignment-1", Generation: 7}
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence}, http.StatusConflict, WorkerErrorInvalidState)

	config := LocalConfig()
	assignment := WorkerAssignment{
		WorkerFence: fence,
		WorkerID:    1,
		WorkerCount: uint64(config.Workload.Workers),
		Config:      config,
	}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", assignment)
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", assignment, http.StatusConflict, WorkerErrorAssignmentConflict)

	wrongFence := fence
	wrongFence.Generation++
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: wrongFence}, http.StatusConflict, WorkerErrorFenceMismatch)
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})
	if generation.starts != 1 {
		t.Fatalf("generation starts = %d, want 1", generation.starts)
	}
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence}, http.StatusConflict, WorkerErrorInvalidState)

	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/rate", WorkerRateRequest{
		WorkerFence: fence, RatePerSecond: 120, MaxBurst: 240,
	})
	if generation.rate != 120 || generation.burst != 240 {
		t.Fatalf("rate/burst = %d/%d, want 120/240", generation.rate, generation.burst)
	}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/checkpoint", WorkerCheckpointRequest{WorkerFence: fence})
	if generation.checkpoints != 1 || generation.starts != 1 {
		t.Fatalf("checkpoint/start calls = %d/%d, want 1/1", generation.checkpoints, generation.starts)
	}

	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
	if generation.drains != 1 || generation.stops != 1 {
		t.Fatalf("drain/stop calls = %d/%d, want 1/1", generation.drains, generation.stops)
	}
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/rate", WorkerRateRequest{
		WorkerFence: fence, RatePerSecond: 100, MaxBurst: 200,
	}, http.StatusConflict, WorkerErrorInvalidState)
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
	if generation.drains != 1 || generation.stops != 1 {
		t.Fatalf("idempotent drain/stop calls = %d/%d, want 1/1", generation.drains, generation.stops)
	}
}

type fakeWorkerGeneration struct {
	starts       int
	rate         uint64
	burst        uint64
	checkpoints  int
	drains       int
	stops        int
	snapshot     WorkerSnapshot
	done         chan error
	drainStarted chan struct{}
	drainRelease chan struct{}
	stopped      chan struct{}
	drainErr     error
	doneOnce     sync.Once
}

func newFakeWorkerGeneration() *fakeWorkerGeneration {
	return &fakeWorkerGeneration{done: make(chan error, 1)}
}

func (g *fakeWorkerGeneration) Start(context.Context) error {
	g.starts++
	return nil
}

func (g *fakeWorkerGeneration) UpdateRate(ratePerSecond, maxBurst uint64) error {
	g.rate, g.burst = ratePerSecond, maxBurst
	return nil
}

func (g *fakeWorkerGeneration) Checkpoint() (WorkerSnapshot, error) {
	g.checkpoints++
	return g.snapshot, nil
}

func (g *fakeWorkerGeneration) Drain(ctx context.Context) error {
	g.drains++
	if g.drainStarted != nil {
		close(g.drainStarted)
	}
	if g.drainRelease != nil {
		select {
		case <-g.drainRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.drainErr
}

func (g *fakeWorkerGeneration) Stop() {
	g.stops++
	if g.stopped != nil {
		close(g.stopped)
	}
	g.terminate(nil)
}

func (g *fakeWorkerGeneration) Snapshot() WorkerSnapshot { return g.snapshot }

func (g *fakeWorkerGeneration) Done() <-chan error { return g.done }

func (g *fakeWorkerGeneration) terminate(err error) {
	g.doneOnce.Do(func() {
		g.done <- err
		close(g.done)
	})
}

func assertWorkerSuccess(t *testing.T, server http.Handler, method, path string, body any) {
	t.Helper()
	response := workerRequest(t, server, method, path, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusOK, response.Body.String())
	}
}

func assertWorkerError(t *testing.T, server http.Handler, method, path string, body any, status int, code WorkerErrorCode) {
	t.Helper()
	response := workerRequest(t, server, method, path, body)
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, status, response.Body.String())
	}
	var apiError WorkerAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if apiError.Code != code {
		t.Fatalf("error code = %q, want %q", apiError.Code, code)
	}
}

func workerRequest(t *testing.T, server http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded strings.Builder
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(encoded.String()))
	req.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	return response
}
