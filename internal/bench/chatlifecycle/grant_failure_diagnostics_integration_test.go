//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Failure cases are frozen before implementation in the task's DESIGN.md:
// request/body deadlines, causal parent cancellation, transport and HTTP errors,
// read/decode/size guards, valid JSON with invalid or mismatched typed results,
// opaque ports, concurrent failures, and cleanup that cannot publish a final.
// The public HTTP seam must retain a closed diagnostic without changing the
// original one-second measured budget, exact grant, retries, or verdict.
func TestGrantDeliveryDiagnosticsHTTPFailuresKeepOriginalControlSemantics(t *testing.T) {
	tests := []struct {
		mode     string
		phase    string
		reason   string
		status   int
		attempts int
		deadline string
	}{
		{mode: "request_deadline", phase: "request", reason: "deadline", attempts: 1, deadline: "round"},
		{mode: "body_deadline", phase: "body", reason: "deadline", status: 200, attempts: 1, deadline: "round"},
		{mode: "transport", phase: "request", reason: "transport", attempts: 2},
		{mode: "client_timeout", phase: "request", reason: "transport", attempts: 2},
		{mode: "api_error", phase: "response", reason: "http_status", status: 409, attempts: 1},
		{mode: "invalid_api_error", phase: "body", reason: "body_decode", status: 503, attempts: 2},
		{mode: "body_read", phase: "body", reason: "body_read", status: 200, attempts: 2},
		{mode: "body_limit", phase: "body", reason: "body_limit", status: 200, attempts: 2},
		{mode: "malformed", phase: "body", reason: "body_decode", status: 200, attempts: 2},
		{mode: "unknown_field", phase: "body", reason: "body_decode", status: 200, attempts: 2},
		{mode: "trailing_json", phase: "body", reason: "body_decode", status: 200, attempts: 2},
		{mode: "invalid_count", phase: "response", reason: "identity", status: 200, attempts: 2},
		{mode: "invalid_sequence", phase: "response", reason: "sequence", status: 200, attempts: 2},
		{mode: "fence", phase: "response", reason: "identity", attempts: 1},
		{mode: "worker", phase: "response", reason: "identity", attempts: 1},
		{mode: "sequence", phase: "response", reason: "sequence", attempts: 1},
		{mode: "released", phase: "response", reason: "released", attempts: 1},
		{mode: "opaque", phase: "worker", reason: "unknown", attempts: 2},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			fixture := newGrantDiagnosticHTTPFixture(t, test.mode, 2, [3]bool{false, true, false})
			result := fixture.run(t)
			if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureDelivery {
				t.Fatalf("original grant classification changed: outcome=%s code=%s grant=%s", result.Outcome, result.Code, result.GrantFailure)
			}
			rows := grantDiagnosticRows(t, result)
			want := grantDiagnosticRow{WorkerID: 1, Sequence: 2, Attempts: test.attempts, Phase: test.phase, Reason: test.reason, HTTPStatus: test.status, DeadlineSource: test.deadline}
			if len(rows) != 1 || rows[0] != want {
				t.Fatalf("grant failure evidence = %+v, want %+v", rows, want)
			}
			fixture.assertExactRequestsAndCleanup(t, test.attempts)
		})
	}
}

func TestGrantDeliveryDiagnosticsParentCancellationKeepsItsCausalBoundary(t *testing.T) {
	for _, mode := range []string{"request_canceled", "body_canceled"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newGrantDiagnosticHTTPFixture(t, mode, 2, [3]bool{false, true, false})
			result := fixture.run(t)
			if result.Outcome != CoordinatorStopped || result.Code != CoordinatorCodeStopped || result.GrantFailure != CoordinatorGrantFailureDelivery {
				t.Fatalf("causal parent cancellation changed classification: outcome=%s code=%s grant=%s", result.Outcome, result.Code, result.GrantFailure)
			}
			rows := grantDiagnosticRows(t, result)
			phase := "request"
			if mode == "body_canceled" {
				phase = "body"
			}
			want := grantDiagnosticRow{WorkerID: 1, Sequence: 2, Attempts: 1, Phase: phase, Reason: "canceled", DeadlineSource: "parent"}
			if phase == "body" {
				want.HTTPStatus = 200
			}
			if len(rows) != 1 || rows[0] != want {
				t.Fatalf("parent cancellation evidence = %+v, want %+v", rows, want)
			}
			fixture.assertCleanup(t)
		})
	}
}

func TestGrantDeliveryDiagnosticsRetainAllThreeFailuresInWorkerOrder(t *testing.T) {
	fixture := newGrantDiagnosticHTTPFixture(t, "api_error", 2, [3]bool{true, true, true})
	result := fixture.run(t)
	rows := grantDiagnosticRows(t, result)
	if len(rows) != coordinatorWorkerCount {
		t.Fatalf("retained failures=%d, want all three workers", len(rows))
	}
	for workerID, row := range rows {
		want := grantDiagnosticRow{WorkerID: uint64(workerID), Sequence: 2, Attempts: 1, Phase: "response", Reason: "http_status", HTTPStatus: 409}
		if row != want {
			t.Fatalf("worker-position %d = %+v, want %+v", workerID, row, want)
		}
	}
	fixture.assertCleanup(t)
}

func TestGrantDeliveryDiagnosticsInitialGrantKeepsItsControlRound(t *testing.T) {
	fixture := newGrantDiagnosticHTTPFixture(t, "api_error", 1, [3]bool{false, true, false})
	result := fixture.run(t)
	rows := grantDiagnosticRows(t, result)
	if len(rows) != 1 || rows[0].Sequence != 1 || rows[0].WorkerID != 1 || rows[0].Reason != "http_status" {
		t.Fatalf("initial grant lost its own failure evidence: %+v", rows)
	}
	fixture.assertExactRequestsAndCleanup(t, 1)
}

func TestGrantDeliveryDiagnosticsLateStatusCannotEraseDeliveryBoundary(t *testing.T) {
	fixture := newGrantDiagnosticHTTPFixture(t, "late_runtime", 2, [3]bool{false, true, false})
	result := fixture.run(t)
	if result.WorkerFailure.WorkerID != 1 || result.WorkerFailure.RuntimeCode != RuntimeFailureTransportAdmissionSaturated {
		t.Fatal("existing late runtime classification was discarded")
	}
	rows := grantDiagnosticRows(t, result)
	want := grantDiagnosticRow{WorkerID: 1, Sequence: 2, Attempts: 1, Phase: "request", Reason: "deadline", DeadlineSource: "round"}
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("late status replaced original grant failure evidence: %+v", rows)
	}
	fixture.assertExactRequestsAndCleanup(t, 1)
}

func TestGrantDeliveryDiagnosticsLateParentCancelCannotRelabelTransportFailure(t *testing.T) {
	fixture := newGrantDiagnosticHTTPFixture(t, "transport_then_cancel", 2, [3]bool{false, true, false})
	result := fixture.run(t)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatal("later parent cancellation erased the original stage failure")
	}
	rows := grantDiagnosticRows(t, result)
	found := false
	for _, row := range rows {
		if row.WorkerID == 1 {
			found = true
			if row.Phase != "request" || row.Reason != "transport" || row.Attempts != 1 || row.DeadlineSource != "" {
				t.Fatalf("ordinary transport failure was relabeled by later cancellation: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("ordinary transport failure disappeared after later parent cancellation")
	}
	fixture.assertCleanup(t)
}

func TestGrantDeliveryDiagnosticsRecoveredRetryKeepsNoFailureHistory(t *testing.T) {
	fixture := newGrantDiagnosticHTTPFixture(t, "recovered_transport", 2, [3]bool{false, true, false})
	result := fixture.run(t)
	if result.Outcome != CoordinatorStopped {
		t.Fatal("recovered retry did not retain ordinary parent-stop semantics")
	}
	if rows := grantDiagnosticRows(t, result); len(rows) != 0 {
		t.Fatalf("successful retry retained stale failure history: %+v", rows)
	}
	fixture.assertExactRequestsAndCleanup(t, 2)
}

type grantDiagnosticRow struct {
	WorkerID       uint64 `json:"worker_id"`
	Sequence       uint64 `json:"sequence"`
	Attempts       int    `json:"attempts"`
	Phase          string `json:"phase"`
	Reason         string `json:"reason"`
	HTTPStatus     int    `json:"http_status"`
	DeadlineSource string `json:"deadline_source"`
}

// Reflection keeps the frozen baseline compilable: the RED symptom is the
// discarded boundary evidence, not an undefined proposed production type.
func grantDiagnosticRows(t *testing.T, result CoordinatorResult) []grantDiagnosticRow {
	t.Helper()
	field := reflect.ValueOf(result).FieldByName("GrantFailures")
	if !field.IsValid() {
		t.Fatal("CoordinatorResult discarded per-worker grant delivery failure evidence")
	}
	if field.Kind() != reflect.Array || field.Len() != coordinatorWorkerCount {
		t.Fatal("grant failure evidence must have exactly three fixed worker positions")
	}
	encoded, err := json.Marshal(field.Interface())
	if err != nil {
		t.Fatal("grant failure evidence cannot be serialized")
	}
	if len(encoded) > 1024 || strings.Contains(string(encoded), grantDiagnosticPrivateCanary) {
		t.Fatal("grant failure evidence is unbounded or contains private input")
	}
	var fixed []grantDiagnosticRow
	if err := json.Unmarshal(encoded, &fixed); err != nil {
		t.Fatal("grant failure evidence does not have the closed local shape")
	}
	rows := make([]grantDiagnosticRow, 0, coordinatorWorkerCount)
	for workerID, row := range fixed {
		if row.Reason == "" {
			continue
		}
		if row.WorkerID != uint64(workerID) || row.Sequence == 0 || row.Attempts < 1 || row.Attempts > 2 {
			t.Fatal("grant failure evidence changed worker identity, sequence or attempt bound")
		}
		rows = append(rows, row)
	}
	artifact := filepath.Join(t.TempDir(), "grant-delivery-diagnostics.json")
	if err := os.WriteFile(artifact, append(encoded, '\n'), 0600); err != nil {
		t.Fatal("cannot retain diagnostic regression artifact")
	}
	t.Logf("temporary diagnostic file: %s", artifact)
	// The caller archives verbose stdout with source hashes; TempDir alone
	// disappears at test completion and is not retained acceptance evidence.
	t.Logf("grant-delivery-evidence=%s", encoded)
	return rows
}

const grantDiagnosticPrivateCanary = "CANARY_private_header_token_body_error"

type grantDiagnosticRequest struct {
	workerID uint64
	request  WorkerGrantRequest
	deadline time.Time
	at       time.Time
}

type grantDiagnosticCompletion struct {
	workerID uint64
	sequence uint64
	success  bool
}

type grantDiagnosticHTTPFixture struct {
	mode         string
	failSequence uint64
	failing      [coordinatorWorkerCount]bool
	workers      []CoordinatorWorker
	clock        *manualCoordinatorClock
	hooks        *recordingCoordinatorRunHooks
	blocked      chan struct{}
	release      chan struct{}
	returned     chan grantDiagnosticCompletion
	parentCancel context.CancelFunc
	mu           sync.Mutex
	assignments  [coordinatorWorkerCount]WorkerAssignment
	requests     []grantDiagnosticRequest
	grantCalls   [coordinatorWorkerCount]int
	stopCalls    [coordinatorWorkerCount]int
	badFence     bool
}

func newGrantDiagnosticHTTPFixture(t *testing.T, mode string, sequence uint64, failing [3]bool) *grantDiagnosticHTTPFixture {
	t.Helper()
	f := &grantDiagnosticHTTPFixture{
		mode: mode, failSequence: sequence, failing: failing,
		workers: make([]CoordinatorWorker, coordinatorWorkerCount),
		clock:   newManualCoordinatorClock(time.Unix(1_700_000_000, 0)),
		hooks:   newRecordingCoordinatorRunHooks(), blocked: make(chan struct{}, coordinatorWorkerCount), release: make(chan struct{}),
		returned: make(chan grantDiagnosticCompletion, coordinatorWorkerCount*4),
	}
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.serveHTTP(t, uint64(workerID), w, r)
		}))
		t.Cleanup(server.Close)
		httpClient := server.Client()
		if mode == "client_timeout" {
			httpClient.Timeout = 100 * time.Millisecond
		}
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: server.URL, ControlToken: grantDiagnosticPrivateCanary, HTTPClient: httpClient, MaxResponseBytes: 1024,
		})
		if err != nil {
			t.Fatal("invalid HTTP worker fixture")
		}
		f.workers[workerID] = &grantDiagnosticRecordingClient{WorkerClient: client, fixture: f, workerID: uint64(workerID)}
	}
	// Cleanup runs in reverse order, so unblock handlers before server.Close.
	t.Cleanup(func() { close(f.release) })
	return f
}

func (f *grantDiagnosticHTTPFixture) run(t *testing.T) CoordinatorResult {
	t.Helper()
	cfg := LocalConfig()
	cfg.RunID = "grant-delivery-diagnostic"
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 37, Workers: f.workers, Clock: f.clock, Hooks: f.hooks,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
	})
	if err != nil {
		t.Fatal("invalid coordinator fixture")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.parentCancel = cancel
	done := make(chan CoordinatorResult, 1)
	go func() { done <- coordinator.Run(ctx, cfg) }()
	guard := time.NewTimer(4 * time.Second)
	defer guard.Stop()
	if f.failSequence > 1 {
		for ticker := 0; ticker < 2; ticker++ {
			select {
			case <-f.clock.created:
			case <-done:
				t.Fatal("HTTP fixture failed before its measured grant")
			case <-guard.C:
				t.Fatal("HTTP fixture did not reach measured grant")
			}
		}
		f.clock.advance(coordinatorGrantCadence)
	}
	if strings.HasSuffix(f.mode, "_canceled") {
		select {
		case <-f.blocked:
		case <-guard.C:
			t.Fatal("HTTP fixture never entered the canceled boundary")
		}
		// Establish the two successful siblings before canceling the parent;
		// a dispatch race must not manufacture extra canceled workers.
		var settled [coordinatorWorkerCount]bool
		for remaining := 2; remaining > 0; {
			select {
			case completion := <-f.returned:
				if completion.sequence == f.failSequence && !f.failing[completion.workerID] && completion.success && !settled[completion.workerID] {
					settled[completion.workerID] = true
					remaining--
				}
			case <-guard.C:
				t.Fatal("HTTP fixture siblings did not settle before cancellation")
			}
		}
		cancel()
	}
	if f.mode == "recovered_transport" {
		var settled [coordinatorWorkerCount]bool
		for remaining := coordinatorWorkerCount; remaining > 0; {
			select {
			case completion := <-f.returned:
				if completion.sequence == f.failSequence && completion.success && !settled[completion.workerID] {
					settled[completion.workerID] = true
					remaining--
				}
			case <-guard.C:
				t.Fatal("matching retry did not recover the grant")
			}
		}
		cancel()
	}
	select {
	case result := <-done:
		return result
	case <-guard.C:
		t.Fatal("grant failure or cleanup exceeded the existing bounded round")
		return CoordinatorResult{}
	}
}

type grantDiagnosticRecordingClient struct {
	*WorkerClient
	fixture  *grantDiagnosticHTTPFixture
	workerID uint64
}

func (c *grantDiagnosticRecordingClient) Grant(ctx context.Context, request WorkerGrantRequest) (response WorkerGrantResponse, err error) {
	defer func() {
		c.fixture.returned <- grantDiagnosticCompletion{workerID: c.workerID, sequence: request.Sequence, success: err == nil}
		if c.fixture.mode == "transport_then_cancel" && request.Sequence == c.fixture.failSequence && c.fixture.failing[c.workerID] && err != nil {
			c.fixture.parentCancel()
		}
	}()
	deadline, _ := ctx.Deadline()
	c.fixture.mu.Lock()
	c.fixture.requests = append(c.fixture.requests, grantDiagnosticRequest{workerID: c.workerID, request: request, deadline: deadline, at: time.Now()})
	c.fixture.mu.Unlock()
	if c.fixture.mode == "opaque" && request.Sequence == c.fixture.failSequence && c.fixture.failing[c.workerID] {
		return WorkerGrantResponse{}, errors.New(grantDiagnosticPrivateCanary + " http://private.invalid/?token=private")
	}
	return c.WorkerClient.Grant(ctx, request)
}

func (f *grantDiagnosticHTTPFixture) serveHTTP(t *testing.T, workerID uint64, w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+grantDiagnosticPrivateCanary {
		t.Error("worker fixture did not receive the expected bearer capability")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v1/chat-lifecycle/assign" {
		var assignment WorkerAssignment
		if json.NewDecoder(r.Body).Decode(&assignment) != nil {
			t.Error("worker fixture received an invalid assignment")
			return
		}
		f.mu.Lock()
		f.assignments[workerID] = assignment
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f.status(workerID, WorkerPhaseAssigned))
		return
	}
	switch r.URL.Path {
	case "/v1/chat-lifecycle/start":
		_ = json.NewEncoder(w).Encode(f.status(workerID, WorkerPhaseRunning))
	case "/v1/chat-lifecycle/status":
		status := f.status(workerID, WorkerPhaseRunning)
		if f.mode == "late_runtime" && f.failing[workerID] {
			status.LastGrantSequence, status.LastGrantFailed = f.failSequence, true
			status.LastGrantRuntimeCode = RuntimeFailureTransportAdmissionSaturated
		}
		_ = json.NewEncoder(w).Encode(status)
	case "/v1/chat-lifecycle/stop":
		var stop WorkerStopRequest
		_ = json.NewDecoder(r.Body).Decode(&stop)
		f.mu.Lock()
		f.stopCalls[workerID]++
		f.badFence = f.badFence || !sameWorkerFence(stop.WorkerFence, f.assignments[workerID].WorkerFence)
		f.mu.Unlock()
		// Stop responses are deliberately rejected. The original failure path
		// discards them; this diagnostic change must not manufacture a final.
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(WorkerAPIError{Code: WorkerErrorInvalidState})
	case "/v1/chat-lifecycle/grant":
		var request WorkerGrantRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("worker fixture received an invalid grant")
			return
		}
		released, _ := request.Released.worker(workerID)
		response := WorkerGrantResponse{WorkerFence: request.WorkerFence, WorkerID: workerID, WorkerCount: 3, Sequence: request.Sequence, Released: released}
		if request.Sequence != f.failSequence || !f.failing[workerID] {
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		f.mu.Lock()
		f.grantCalls[workerID]++
		attempt := f.grantCalls[workerID]
		f.mu.Unlock()
		switch f.mode {
		case "request_deadline", "request_canceled", "body_deadline", "body_canceled", "client_timeout", "late_runtime":
			if strings.HasPrefix(f.mode, "body_") {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
			}
			f.blocked <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-f.release:
			}
		case "transport", "transport_then_cancel", "recovered_transport":
			if f.mode == "recovered_transport" && attempt > 1 {
				_ = json.NewEncoder(w).Encode(response)
				return
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("cannot inject closed HTTP transport")
				return
			}
			_ = connection.Close()
		case "api_error":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(WorkerAPIError{Code: WorkerErrorInvalidState})
		case "invalid_api_error":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"` + grantDiagnosticPrivateCanary + `"}`))
		case "body_read":
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte(`{"private":"` + grantDiagnosticPrivateCanary))
		case "body_limit":
			_, _ = w.Write([]byte(strings.Repeat(grantDiagnosticPrivateCanary, 64)))
		case "malformed":
			_, _ = w.Write([]byte(`{"private":"` + grantDiagnosticPrivateCanary))
		case "unknown_field":
			encoded, _ := json.Marshal(response)
			_, _ = w.Write(append(encoded[:len(encoded)-1], []byte(`,"private":"`+grantDiagnosticPrivateCanary+`"}`)...))
		case "trailing_json":
			_ = json.NewEncoder(w).Encode(response)
			_, _ = w.Write([]byte(`{"private":"` + grantDiagnosticPrivateCanary + `"}`))
		case "invalid_count", "invalid_sequence", "fence", "worker", "sequence", "released":
			switch f.mode {
			case "invalid_count":
				response.WorkerCount = 4
			case "invalid_sequence":
				response.Sequence = 0
			case "fence":
				response.AssignmentID = grantDiagnosticPrivateCanary
			case "worker":
				response.WorkerID = (workerID + 1) % 3
			case "sequence":
				response.Sequence++
			case "released":
				response.Released++
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			t.Error("unregistered HTTP failure fixture")
		}
	default:
		t.Error("unexpected worker control endpoint")
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *grantDiagnosticHTTPFixture) status(workerID uint64, phase WorkerPhase) WorkerStatus {
	f.mu.Lock()
	assignment := f.assignments[workerID]
	f.mu.Unlock()
	return WorkerStatus{RunID: assignment.RunID, AssignmentID: assignment.AssignmentID, Generation: assignment.Generation, WorkerID: workerID, WorkerCount: 3, Phase: phase, TrafficReady: true}
}

func (f *grantDiagnosticHTTPFixture) assertExactRequestsAndCleanup(t *testing.T, failedAttempts int) {
	t.Helper()
	f.mu.Lock()
	requests := append([]grantDiagnosticRequest(nil), f.requests...)
	f.mu.Unlock()
	var counts [coordinatorWorkerCount]int
	var exact WorkerGrantRequest
	var sharedDeadline time.Time
	for _, observed := range requests {
		if observed.request.Sequence != f.failSequence {
			continue
		}
		counts[observed.workerID]++
		if exact.Sequence == 0 {
			exact, sharedDeadline = observed.request, observed.deadline
		}
		if observed.request != exact || observed.deadline.IsZero() || !observed.deadline.Equal(sharedDeadline) {
			t.Fatal("grant diagnostics changed the exact shared grant or concurrent deadline")
		}
		maximum := coordinatorDefaultRoundTime
		if f.failSequence > 1 {
			maximum = coordinatorGrantCadence
		}
		remaining := observed.deadline.Sub(observed.at)
		if remaining <= 0 || remaining > maximum {
			t.Fatalf("grant deadline remaining=%s, want (0,%s]", remaining, maximum)
		}
	}
	for workerID, count := range counts {
		want := 1
		if f.failing[workerID] {
			want = failedAttempts
		}
		if count != want {
			t.Fatalf("worker %d attempts=%d, want original retry count %d", workerID, count, want)
		}
	}
	f.assertCleanup(t)
}

func (f *grantDiagnosticHTTPFixture) assertCleanup(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	stops, badFence := f.stopCalls, f.badFence
	f.mu.Unlock()
	if stops != [3]int{1, 1, 1} || badFence {
		t.Fatalf("failure cleanup stops=%v, bad fence=%t", stops, badFence)
	}
	select {
	case <-f.hooks.finalized:
		t.Fatal("grant diagnostics fabricated a completed native finalization")
	default:
	}
	if f.failSequence > 1 {
		select {
		case <-f.hooks.started:
		default:
			t.Fatalf("sequence %d was not a measured grant", f.failSequence)
		}
	}
}
