package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
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
		{http.MethodPost, "/v1/chat-lifecycle/grant", `{}`},
		{http.MethodPost, "/v1/chat-lifecycle/lifecycle-candidates", `{}`},
		{http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", `{}`},
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

func TestWorkerServerLifecycleCandidateLeaseIsBoundedFencedAndTransient(t *testing.T) {
	now := time.Unix(2_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	generation := &fakeLifecycleLeaseGeneration{fakeWorkerGeneration: newFakeWorkerGeneration(), candidates: []LifecycleCandidate{candidate}}
	server, fence := startWorkerServerForGeneration(t, generation, "lifecycle-lease")

	response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-candidates", WorkerLifecycleCandidateLeaseRequest{WorkerFence: fence, Requested: 1})
	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	var lease WorkerLifecycleCandidateLeaseResponse
	if err := json.Unmarshal(response.Body.Bytes(), &lease); err != nil {
		t.Fatal(err)
	}
	if len(lease.Candidates) != 1 || lease.Candidates[0] != candidate || generation.requested != 1 {
		t.Fatalf("lease = %+v, requested=%d", lease, generation.requested)
	}
	approveResponse := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", WorkerLifecycleReheatRequest{
		WorkerFence: fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion,
	})
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("approve status/body = %d/%s", approveResponse.Code, approveResponse.Body.String())
	}
	var approved WorkerLifecycleReheatResponse
	if err := json.Unmarshal(approveResponse.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	if !approved.Approved || generation.approved != candidate.ChannelID || generation.approvedToken != candidate.TimerToken || generation.approvedVersion != candidate.ActivityVersion {
		t.Fatalf("approved=%+v identity match=%v", approved, generation.approved == candidate.ChannelID)
	}

	status := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("snapshot status/body = %d/%s", status.Code, status.Body.String())
	}
	if strings.Contains(status.Body.String(), candidate.ChannelID) || strings.Contains(status.Body.String(), "timer_token") || strings.Contains(status.Body.String(), "activity_version") {
		t.Fatal("transient lifecycle lease data leaked into snapshot")
	}
}

func TestWorkerServerLifecycleCandidateLeaseRejectsOversizeFencePhaseAndInvalidProviderRows(t *testing.T) {
	now := time.Unix(2_000, 0)
	valid := lifecycleTestCandidates(t, now)[0]
	for _, test := range []struct {
		name    string
		start   bool
		request WorkerLifecycleCandidateLeaseRequest
		rows    []LifecycleCandidate
		want    WorkerErrorCode
	}{
		{"oversize", true, WorkerLifecycleCandidateLeaseRequest{Requested: 1201}, nil, WorkerErrorInvalidRequest},
		{"wrong fence", true, WorkerLifecycleCandidateLeaseRequest{Requested: 1, WorkerFence: WorkerFence{RunID: "other", AssignmentID: "other", Generation: 9}}, []LifecycleCandidate{valid}, WorkerErrorFenceMismatch},
		{"not running", false, WorkerLifecycleCandidateLeaseRequest{Requested: 1}, []LifecycleCandidate{valid}, WorkerErrorInvalidState},
		{"provider exceeds requested", true, WorkerLifecycleCandidateLeaseRequest{Requested: 1}, []LifecycleCandidate{valid, valid}, WorkerErrorRuntimeFailure},
		{"provider duplicate", true, WorkerLifecycleCandidateLeaseRequest{Requested: 2}, []LifecycleCandidate{valid, valid}, WorkerErrorRuntimeFailure},
		{"provider invalid raw", true, WorkerLifecycleCandidateLeaseRequest{Requested: 1}, []LifecycleCandidate{{ChannelID: "private-invalid"}}, WorkerErrorRuntimeFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation := &fakeLifecycleLeaseGeneration{fakeWorkerGeneration: newFakeWorkerGeneration(), candidates: test.rows}
			var server *WorkerServer
			var fence WorkerFence
			if test.start {
				server, fence = startWorkerServerForGeneration(t, generation, "lifecycle-lease-"+test.name)
			} else {
				var err error
				server, err = NewWorkerServer(WorkerServerConfig{ControlToken: "control-secret", Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return generation, nil })})
				if err != nil {
					t.Fatal(err)
				}
				config := LocalConfig()
				fence = WorkerFence{RunID: config.RunID, AssignmentID: "lifecycle-lease-not-running", Generation: 7}
				assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config})
			}
			request := test.request
			if request.RunID == "" {
				request.WorkerFence = fence
			}
			response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-candidates", request)
			var apiErr WorkerAPIError
			_ = json.Unmarshal(response.Body.Bytes(), &apiErr)
			if apiErr.Code != test.want {
				t.Fatalf("status/body = %d/%s, want %s", response.Code, response.Body.String(), test.want)
			}
		})
	}
}

func TestWorkerServerLifecycleCandidateLeaseRejectsUnknownJSONFields(t *testing.T) {
	server, fence := startWorkerServerForGeneration(t, &fakeLifecycleLeaseGeneration{fakeWorkerGeneration: newFakeWorkerGeneration()}, "lifecycle-unknown")
	body := fmt.Sprintf(`{"run_id":%q,"assignment_id":%q,"generation":%d,"requested":1,"future":true}`, fence.RunID, fence.AssignmentID, fence.Generation)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat-lifecycle/lifecycle-candidates", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), string(WorkerErrorInvalidJSON)) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestWorkerServerLifecycleReheatRejectsFenceMissingAndHonorsCancellation(t *testing.T) {
	now := time.Unix(2_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	falseValue := false
	generation := &fakeLifecycleLeaseGeneration{fakeWorkerGeneration: newFakeWorkerGeneration(), candidates: []LifecycleCandidate{candidate}, approveResult: &falseValue}
	server, fence := startWorkerServerForGeneration(t, generation, "lifecycle-reheat-errors")
	wrong := fence
	wrong.Generation++
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", WorkerLifecycleReheatRequest{WorkerFence: wrong, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion}, http.StatusConflict, WorkerErrorFenceMismatch)
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion}, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, ActivityVersion: candidate.ActivityVersion}, http.StatusBadRequest, WorkerErrorInvalidRequest)

	generation.approveResult = nil
	generation.approveCompleted = false
	generation.approveEntered = make(chan struct{})
	generation.approveRelease = make(chan struct{})
	requestCtx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- workerRequestWithContext(t, server, requestCtx, http.MethodPost, "/v1/chat-lifecycle/lifecycle-reheat", WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion})
	}()
	<-generation.approveEntered
	cancel()
	<-done
	if generation.approveCompleted {
		t.Fatal("canceled approval crossed admission")
	}
}

func TestWorkerServerAdvertisesCoordinatorGrantProtocolV2(t *testing.T) {
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

	request := httptest.NewRequest(http.MethodGet, "/v1/info", nil)
	request.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var info WorkerInfo
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.ProtocolVersion != 2 {
		t.Fatalf("protocol version = %d, want 2", info.ProtocolVersion)
	}
}

func TestWorkerServerCanceledAssignmentCannotInstallAfterFactoryReturns(t *testing.T) {
	generation := newFakeWorkerGeneration()
	factoryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			close(factoryEntered)
			<-releaseFactory
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("new worker server: %v", err)
	}
	config := LocalConfig()
	assignment := WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "canceled-assignment", Generation: 1},
		WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), CoordinatorGrants: true, Config: config,
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	assignDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		assignDone <- workerRequestWithContext(t, server, requestContext, http.MethodPost, "/v1/chat-lifecycle/assign", assignment)
	}()
	<-factoryEntered
	cancelRequest()
	close(releaseFactory)
	<-assignDone

	statusResponse := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/status", nil)
	var status WorkerStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Phase != WorkerPhaseUnassigned {
		t.Fatalf("phase after canceled assignment = %s, want unassigned", status.Phase)
	}
	if generation.stops != 1 {
		t.Fatalf("discarded generation stops = %d, want 1 cleanup", generation.stops)
	}
}

func TestWorkerServerCanceledStartCannotLeaveStoppedGenerationAssigned(t *testing.T) {
	generation := &blockingStartWorkerGeneration{
		fakeWorkerGeneration: newFakeWorkerGeneration(),
		startEntered:         make(chan struct{}),
		startRelease:         make(chan struct{}),
	}
	generation.stopped = make(chan struct{})
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
	assignment := WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "canceled-start", Generation: 1},
		WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), CoordinatorGrants: true, Config: config,
	}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", assignment)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	startDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		startDone <- workerRequestWithContext(
			t, server, requestContext, http.MethodPost, "/v1/chat-lifecycle/start",
			WorkerStartRequest{WorkerFence: assignment.WorkerFence},
		)
	}()
	<-generation.startEntered
	cancelRequest()
	close(generation.startRelease)
	<-startDone
	server.mu.Lock()
	stopTask := server.stop
	server.mu.Unlock()
	if stopTask == nil {
		t.Fatal("canceled late-success Start did not install server-owned cleanup")
	}
	<-stopTask.done

	statusResponse := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/status", nil)
	var status WorkerStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Phase != WorkerPhaseFinal {
		t.Fatalf("phase after canceled late-success Start = %s, want final", status.Phase)
	}
}

func TestWorkerServerAppliesCoordinatorGrantOnceAndFencesSequence(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
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
	assignment := WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "grant-sequence", Generation: 1},
		WorkerID:    1, WorkerCount: uint64(config.Workload.Workers), CoordinatorGrants: true, Config: config,
	}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", assignment)
	fence := assignment.WorkerFence
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})

	grant := WorkerGrantRequest{
		WorkerFence:   fence,
		Sequence:      1,
		RatePerSecond: 120,
		MaxBurst:      240,
		Fresh:         WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
		Released:      WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
		Credit:        WorkerGrantCounts{},
	}
	first := assertWorkerGrantSuccess(t, server, grant)
	duplicate := assertWorkerGrantSuccess(t, server, grant)
	if first != duplicate {
		t.Fatalf("duplicate grant response = %+v, want stable %+v", duplicate, first)
	}
	if got := generation.grants; !reflect.DeepEqual(got, []uint64{40}) {
		t.Fatalf("applied grants = %v, want one worker-local release", got)
	}

	gap := grant
	gap.Sequence = 3
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/grant", gap, http.StatusConflict, WorkerErrorGrantGap)
	conflict := grant
	conflict.Released.Worker1--
	conflict.Credit.Worker1++
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/grant", conflict, http.StatusConflict, WorkerErrorGrantConflict)
	second := grant
	second.Sequence = 2
	assertWorkerGrantSuccess(t, server, second)
	assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/grant", grant, http.StatusConflict, WorkerErrorGrantStale)
	if got := generation.grants; !reflect.DeepEqual(got, []uint64{40, 40}) {
		t.Fatalf("grant applications = %v, want sequences 1 and 2 once each", got)
	}
}

func TestWorkerServerCachesAcceptedGrantFailureWithoutRegeneration(t *testing.T) {
	t.Parallel()

	generation := newFakeWorkerGeneration()
	generation.grantErr = errors.New("injected accepted grant failure")
	server, fence := startWorkerServerForCoordinatorGeneration(t, generation, "grant-failure")
	grant := WorkerGrantRequest{
		WorkerFence: fence, Sequence: 1, RatePerSecond: 120, MaxBurst: 240,
		Fresh:    WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
		Released: WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
	}
	for attempt := 0; attempt < 2; attempt++ {
		assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/grant", grant, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
	}
	if got := generation.grants; !reflect.DeepEqual(got, []uint64{40}) {
		t.Fatalf("failed grant applications = %v, want one accepted attempt", got)
	}
}

func TestWorkerServerCanceledGrantBeforeAdmissionAllowsSameSequenceRetry(t *testing.T) {
	t.Parallel()

	generation := &cancelBeforeGrantAdmissionGeneration{
		fakeWorkerGeneration: newFakeWorkerGeneration(),
		firstEntered:         make(chan struct{}),
	}
	server, fence := startWorkerServerForCoordinatorGeneration(t, generation, "grant-pre-admission-cancel")
	grant := WorkerGrantRequest{
		WorkerFence: fence, Sequence: 1, RatePerSecond: 120, MaxBurst: 240,
		Fresh:    WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
		Released: WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
	}

	requestContext, cancelRequest := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_ = workerRequestWithContext(
			t, server, requestContext, http.MethodPost, "/v1/chat-lifecycle/grant", grant,
		)
	}()
	<-generation.firstEntered
	cancelRequest()
	<-firstDone

	result := assertWorkerGrantSuccess(t, server, grant)
	if result.Sequence != grant.Sequence || result.Released != grant.Released.Worker0 {
		t.Fatalf("retried grant response = %+v, want accepted sequence 1", result)
	}
	if got := generation.grants; !reflect.DeepEqual(got, []uint64{40}) {
		t.Fatalf("admitted grants = %v, want only successful retry", got)
	}
}

func assertWorkerGrantSuccess(t *testing.T, server http.Handler, grant WorkerGrantRequest) WorkerGrantResponse {
	t.Helper()
	response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/grant", grant)
	if response.Code != http.StatusOK {
		t.Fatalf("grant status = %d, want %d; body = %q", response.Code, http.StatusOK, response.Body.String())
	}
	var result WorkerGrantResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode grant response: %v", err)
	}
	return result
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
	if snapshot.Phase != WorkerPhaseFinal || !snapshot.Harness.DrainTimedOut || snapshot.Harness.Classification != SyncClassificationHarnessInvalid ||
		snapshot.Evidence.Classification != SyncClassificationHarnessInvalid || snapshot.Harness.Failures == 0 {
		t.Fatalf("timeout final snapshot = %+v", snapshot)
	}
	if generation.drains != 1 || generation.stops != 1 {
		t.Fatalf("drain/stop calls = %d/%d, want 1/1", generation.drains, generation.stops)
	}
}

func TestWorkerServerCountsDrainAndFinalSnapshotFailuresIndependently(t *testing.T) {
	for _, test := range []struct {
		name             string
		drainErr         error
		snapshotErr      error
		wantFailures     uint64
		wantDrainTimeout bool
	}{
		{
			name: "snapshot timeout is not a drain timeout", snapshotErr: context.DeadlineExceeded,
			wantFailures: 6,
		},
		{
			name: "drain and snapshot failures count separately", drainErr: context.DeadlineExceeded,
			snapshotErr: errors.New("redacted snapshot failure"), wantFailures: 7, wantDrainTimeout: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation := newFakeWorkerGeneration()
			generation.drainErr = test.drainErr
			generation.snapshotErr = test.snapshotErr
			generation.snapshot.Harness.Failures = 5
			server, fence := startFakeWorkerServer(t, generation, "independent-final-errors")

			response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
			if response.Code != http.StatusOK {
				t.Fatalf("stop status = %d; body = %q", response.Code, response.Body.String())
			}
			var snapshot WorkerSnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatalf("decode stop snapshot: %v", err)
			}
			if snapshot.Harness.Failures != test.wantFailures || snapshot.Harness.DrainTimedOut != test.wantDrainTimeout {
				t.Fatalf("final harness = %+v, want failures=%d drain_timed_out=%v", snapshot.Harness, test.wantFailures, test.wantDrainTimeout)
			}
			if snapshot.Harness.Classification != SyncClassificationHarnessInvalid ||
				snapshot.Evidence.Classification != SyncClassificationHarnessInvalid {
				t.Fatalf("final classifications disagree: harness=%q evidence=%q", snapshot.Harness.Classification, snapshot.Evidence.Classification)
			}
		})
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
	if snapshot.Phase != WorkerPhaseFinal || !snapshot.Harness.UnexpectedExit || snapshot.Harness.Classification != SyncClassificationHarnessInvalid ||
		snapshot.Evidence.Classification != SyncClassificationHarnessInvalid || snapshot.Harness.Failures == 0 {
		t.Fatalf("unexpected final snapshot = %+v", snapshot)
	}
}

func TestWorkerServerFinalClassificationPreservesProductPrecedence(t *testing.T) {
	for _, test := range []struct {
		name       string
		unexpected bool
		invalid    bool
	}{
		{name: "drain timeout"},
		{name: "unexpected exit", unexpected: true},
		{name: "invalid final snapshot", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation := newFakeWorkerGeneration()
			generation.snapshot.Evidence.Classification = SyncClassificationProductFailure
			generation.snapshot.Harness.Classification = SyncClassificationHarnessInvalid
			if test.invalid {
				generation.snapshot.Evidence.Classes = make([]EvidenceClassSnapshot, int(FailureClassHarness)+1)
			}
			server, fence := startFakeWorkerServer(t, generation, "product-"+strings.ReplaceAll(test.name, " ", "-"))

			var snapshot WorkerSnapshot
			if test.unexpected {
				generation.terminate(errors.New("redacted unexpected failure"))
				<-server.UnexpectedExit()
				response := workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil)
				if response.Code != http.StatusOK {
					t.Fatalf("snapshot status = %d; body = %q", response.Code, response.Body.String())
				}
				if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
					t.Fatalf("decode unexpected snapshot: %v", err)
				}
			} else {
				generation.drainErr = context.DeadlineExceeded
				response := workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
				if response.Code != http.StatusOK {
					t.Fatalf("stop status = %d; body = %q", response.Code, response.Body.String())
				}
				if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
					t.Fatalf("decode stop snapshot: %v", err)
				}
			}

			if snapshot.Harness.Classification != SyncClassificationProductFailure ||
				snapshot.Evidence.Classification != SyncClassificationProductFailure {
				t.Fatalf("product classification was downgraded: %+v", snapshot)
			}
			if snapshot.Harness.Failures == 0 {
				t.Fatalf("harness failure was not counted: %+v", snapshot.Harness)
			}
			if test.unexpected != snapshot.Harness.UnexpectedExit {
				t.Fatalf("unexpected flag = %v, want %v", snapshot.Harness.UnexpectedExit, test.unexpected)
			}
			if !test.unexpected && !snapshot.Harness.DrainTimedOut {
				t.Fatalf("drain timeout flag was lost: %+v", snapshot.Harness)
			}
		})
	}
}

func TestMergeSyncClassificationUsesClosedPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		values []SyncClassification
		want   SyncClassification
	}{
		{name: "empty"},
		{name: "harness", values: []SyncClassification{"", SyncClassificationHarnessInvalid}, want: SyncClassificationHarnessInvalid},
		{name: "product over harness", values: []SyncClassification{SyncClassificationHarnessInvalid, SyncClassificationProductFailure}, want: SyncClassificationProductFailure},
		{name: "product independent of order", values: []SyncClassification{SyncClassificationProductFailure, SyncClassificationHarnessInvalid}, want: SyncClassificationProductFailure},
		{name: "unknown fails closed", values: []SyncClassification{"outside-vocabulary"}, want: SyncClassificationHarnessInvalid},
		{name: "product over unknown", values: []SyncClassification{"outside-vocabulary", SyncClassificationProductFailure}, want: SyncClassificationProductFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mergeSyncClassification(test.values...); got != test.want {
				t.Fatalf("mergeSyncClassification(%q) = %q, want %q", test.values, got, test.want)
			}
		})
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
	snapshot, err := generation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("pre-start Snapshot: %v", err)
	}
	if snapshot.WorkerID != 1 || snapshot.WorkerCount != 3 || snapshot.Sessions.Target != 33 {
		t.Fatalf("pre-start engine snapshot = %+v", snapshot)
	}
	if err := generation.UpdateRate(context.Background(), 100, 200); !errors.Is(err, errEngineNotRunning) {
		t.Fatalf("pre-start rate error = %v, want %v", err, errEngineNotRunning)
	}
	engineGeneration.engine.lifecycleMu.Lock()
	engineGeneration.engine.cached.GatewayConnectLatency = newWorkerHistogramSnapshot()
	recordWorkerLatency(&engineGeneration.engine.cached.GatewayConnectLatency, 20*time.Millisecond)
	engineGeneration.engine.cached.ConversationSyncLatency = newWorkerHistogramSnapshot()
	recordWorkerLatency(&engineGeneration.engine.cached.ConversationSyncLatency, 50*time.Millisecond)
	engineGeneration.engine.cached.FactoryFailed = 1
	engineGeneration.engine.cached.FactoryCanceled = 2
	engineGeneration.engine.cached.ConnectStarted = 11
	engineGeneration.engine.cached.ConnectCompleted = 8
	engineGeneration.engine.cached.ConnectFailed = 2
	engineGeneration.engine.cached.ConnectCanceled = 1
	engineGeneration.engine.cached.SyncStarted = 8
	engineGeneration.engine.cached.SyncCompleted = 5
	engineGeneration.engine.cached.SyncFailed = 2
	engineGeneration.engine.cached.SyncCanceled = 1
	engineGeneration.engine.lifecycleMu.Unlock()
	engineGeneration.verifier.sendMu.Lock()
	recordWorkerLatency(&engineGeneration.verifier.sendackLatency, 2*time.Second)
	engineGeneration.verifier.sendMu.Unlock()
	engineGeneration.verifier.recvMu.Lock()
	recordWorkerLatency(&engineGeneration.verifier.recvackLatency, 50*time.Millisecond)
	engineGeneration.verifier.recvMu.Unlock()
	snapshot, err = generation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("latency Snapshot: %v", err)
	}
	if snapshot.Sync.ConnectLatency.Buckets[5] != 1 || snapshot.Sync.Latency.Buckets[6] != 1 ||
		snapshot.SendackLatency.Buckets[11] != 1 || snapshot.RecvackLatency.Buckets[6] != 1 {
		t.Fatalf("worker latency projection = sync=%+v sendack=%+v recvack=%+v", snapshot.Sync, snapshot.SendackLatency, snapshot.RecvackLatency)
	}
	if snapshot.Sync.FactoryFailed != 1 || snapshot.Sync.FactoryCanceled != 2 ||
		snapshot.Sync.ConnectStarted != 11 || snapshot.Sync.ConnectCompleted != 8 || snapshot.Sync.ConnectFailed != 2 || snapshot.Sync.ConnectCanceled != 1 ||
		snapshot.Sync.SyncStarted != 8 || snapshot.Sync.SyncCompleted != 5 || snapshot.Sync.SyncFailed != 2 || snapshot.Sync.SyncCanceled != 1 || snapshot.Sync.Failures != 2 {
		t.Fatalf("worker real sync outcome projection = %+v", snapshot.Sync)
	}
}

func TestWorkerEngineSequenceCapacityCoversPersonAndFixedGroupChannels(t *testing.T) {
	formal := FormalConfig()
	for workerID, want := range []int{36_674, 36_663, 36_663} {
		limits, err := workerEngineLimitsFor(WorkerAssignment{
			WorkerFence: WorkerFence{RunID: formal.RunID, AssignmentID: "sequence-formal", Generation: 1},
			WorkerID:    uint64(workerID), WorkerCount: uint64(formal.Workload.Workers), Config: formal,
		})
		if err != nil {
			t.Fatalf("formal worker %d limits: %v", workerID, err)
		}
		if limits.sequence != want {
			t.Fatalf("formal worker %d sequence capacity = %d, want %d", workerID, limits.sequence, want)
		}
	}

	local := LocalConfig()
	for workerID := range local.Workload.Workers {
		limits, err := workerEngineLimitsFor(WorkerAssignment{
			WorkerFence: WorkerFence{RunID: local.RunID, AssignmentID: "sequence-local", Generation: 1},
			WorkerID:    uint64(workerID), WorkerCount: uint64(local.Workload.Workers), Config: local,
		})
		if err != nil {
			t.Fatalf("local worker %d limits: %v", workerID, err)
		}
		if limits.sequence != 4_096 {
			t.Fatalf("local worker %d sequence capacity = %d, want minimum 4096", workerID, limits.sequence)
		}
	}

	overflow := local
	overflow.Workload.OnlineUsers = int(^uint(0) >> 1)
	overflow.Workload.Workers = 1
	limits, err := workerEngineLimitsFor(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: overflow.RunID, AssignmentID: "sequence-overflow", Generation: 1},
		WorkerID:    0, WorkerCount: 1, Config: overflow,
	})
	if err != nil {
		t.Fatalf("overflow limits: %v", err)
	}
	if limits.sequence != maxVerifierCapacity {
		t.Fatalf("overflow sequence capacity = %d, want cap %d", limits.sequence, maxVerifierCapacity)
	}

	limits, err = workerEngineLimitsFor(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: formal.RunID, AssignmentID: "sequence-verifier", Generation: 1},
		WorkerID:    0, WorkerCount: uint64(formal.Workload.Workers), Config: formal,
	})
	if err != nil {
		t.Fatalf("formal verifier limits: %v", err)
	}
	model := newTestTrafficModel(t, formal)
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder: %v", err)
	}
	verifier, err := NewVerifier(model, VerifierConfig{
		PendingCapacity: 1, SequenceCapacity: limits.sequence, CorrelationCapacity: 1, CorrelationDeadline: time.Second,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	logical := mustLogicalSend(t, model, 0, 30_000, TrafficGroup, "sequence-sender", "sequence-group")
	recv := mustRecvPacket(t, model, logical, 1, 1)
	for index := 0; index < limits.sequence; index++ {
		recv.MessageID = int64(index + 1)
		recipient := "sequence-member-" + strconv.Itoa(index)
		if err := verifier.HandleRecv(context.Background(), recipient, recv, discardRecvAcker{}); err != nil {
			t.Fatalf("HandleRecv within capacity at %d/%d: %v", index, limits.sequence, err)
		}
	}
	if limits.sequence <= 16_670 {
		t.Fatalf("sequence capacity = %d, did not cover the old worker-0 ceiling", limits.sequence)
	}
	recv.MessageID++
	overflowErr := verifier.HandleRecv(context.Background(), "sequence-overflow", recv, discardRecvAcker{})
	assertVerificationCode(t, overflowErr, FailureCodeSequenceCapacity)
	snapshot := verifier.Snapshot()
	if snapshot.SequenceCurrent != limits.sequence || snapshot.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("sequence overflow snapshot = %+v, want current=%d harness_invalid", snapshot, limits.sequence)
	}
}

func TestWorkerEngineTrafficLatchDoesNotPollSessionQueues(t *testing.T) {
	config := LocalConfig()
	config.RunID = "worker-o1-latch"
	config.Workload.OnlineUsers = config.Workload.Workers
	if err := config.Validate(); err != nil {
		t.Fatalf("latch config: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	sessions := &engineFakeFactory{}
	factory := engineWorkerGenerationFactory{
		clock: clock,
		newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) {
			return sessions, nil
		},
		newSyncer: func(WorkerAssignment) (ConversationSyncer, error) {
			return engineSyncer{}, nil
		},
	}
	worker, err := factory.New(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "o1-latch", Generation: 7},
		WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	if err != nil {
		t.Fatalf("New generation: %v", err)
	}
	generation := worker.(*engineWorkerGeneration)
	if err := generation.engine.StartGeneration(context.Background(), 7); err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	defer generation.Stop()
	uid := generation.engine.sessions.identity.UID(0)
	if _, err := generation.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0, LoginOrdinal: 0}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := sessions.clients()[0]
	queueEntered := make(chan struct{}, 1)
	queueRelease := make(chan struct{})
	client.queueSnapshotEntered = queueEntered
	client.queueSnapshotRelease = queueRelease

	stepResult := make(chan error, 1)
	go func() { stepResult <- generation.step(context.Background(), clock.Now()) }()
	select {
	case <-queueEntered:
		close(queueRelease)
		if err := <-stepResult; err != nil {
			t.Fatalf("blocked step: %v", err)
		}
		t.Fatal("traffic latch polled a session QueueSnapshot")
	case err := <-stepResult:
		close(queueRelease)
		if err != nil {
			t.Fatalf("step: %v", err)
		}
	}
	if !generation.trafficReady.Load() {
		t.Fatal("traffic latch did not observe the synchronized local online target")
	}

	// Coordinator polling remains the complete projection and therefore still
	// gathers every transport queue snapshot outside the tick hot path.
	pollEntered := make(chan struct{}, 1)
	pollRelease := make(chan struct{})
	client.queueSnapshotEntered = pollEntered
	client.queueSnapshotRelease = pollRelease
	pollResult := make(chan struct {
		snapshot WorkerSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, snapshotErr := generation.Snapshot(context.Background())
		pollResult <- struct {
			snapshot WorkerSnapshot
			err      error
		}{snapshot: snapshot, err: snapshotErr}
	}()
	<-pollEntered
	close(pollRelease)
	poll := <-pollResult
	if poll.err != nil {
		t.Fatalf("Snapshot: %v", poll.err)
	}
	if poll.snapshot.Sessions.Online != 1 || poll.snapshot.Sessions.Target != 1 {
		t.Fatalf("complete coordinator snapshot = %+v", poll.snapshot.Sessions)
	}
}

func TestWorkerEngineGenerationBootstrapsBeforeTrafficAndUsesAssignedGeneration(t *testing.T) {
	config := LocalConfig()
	config.RunID = "worker-bootstrap"
	config.Workload.OnlineUsers = 12
	config.Workload.NewUsersPerDay = 250_000
	config.Workload.SendRatePerSecond = 10
	config.Workload.MaxGlobalBurst = 20
	config.Workload.Sessions = []DurationShare{{Percent: 100, Min: 3 * time.Hour, Max: 3 * time.Hour}}
	if err := config.Validate(); err != nil {
		t.Fatalf("bootstrap config: %v", err)
	}

	startedAt := time.Unix(1_700_000_000, 0)
	run := func(generation uint64, exerciseChurn bool) string {
		t.Helper()
		clock := &sessionFakeClock{now: startedAt}
		sessions := &engineFakeFactory{}
		syncer := &workerBootstrapSyncer{blockAfter: 4}
		ticker := newManualWorkerGenerationTicker()
		factory := engineWorkerGenerationFactory{
			clock: clock,
			newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) {
				return sessions, nil
			},
			newSyncer: func(WorkerAssignment) (ConversationSyncer, error) {
				return syncer, nil
			},
			newTicker: func(interval time.Duration) workerGenerationTicker {
				if interval != time.Second {
					t.Fatalf("ticker interval = %v, want 1s", interval)
				}
				return ticker
			},
		}
		assignment := WorkerAssignment{
			WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "bootstrap", Generation: generation},
			WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), Config: config,
		}
		worker, err := factory.New(assignment)
		if err != nil {
			t.Fatalf("New generation %d: %v", generation, err)
		}
		engineGeneration := worker.(*engineWorkerGeneration)
		if err := worker.Start(context.Background()); err != nil {
			t.Fatalf("Start generation %d: %v", generation, err)
		}
		defer worker.Stop()
		ticker.awaitReady(t)

		initial, err := worker.Checkpoint(context.Background())
		if err != nil {
			t.Fatalf("initial checkpoint generation %d: %v", generation, err)
		}
		if initial.Generation != generation || initial.Sessions.Online != 0 || initial.Generated.Primary != 0 {
			t.Fatalf("initial generation %d snapshot = %+v", generation, initial)
		}

		clock.Set(startedAt.Add(46 * time.Minute))
		ticker.tickAndWait(t)
		engineGeneration.engine.loginOps.Wait()
		bootstrap, err := worker.Checkpoint(context.Background())
		if err != nil {
			t.Fatalf("bootstrap checkpoint generation %d: %v", generation, err)
		}
		if bootstrap.Sessions.Online != bootstrap.Sessions.Target || bootstrap.Generated.Primary != 0 ||
			bootstrap.Harness.OfferedUnderdelivery != 0 || bootstrap.Harness.Failures != 0 || len(bootstrap.Evidence.Classes) != 0 {
			t.Fatalf("bootstrap leaked traffic/evidence generation %d: %+v", generation, bootstrap)
		}
		requests := syncer.requests()
		if len(requests) != bootstrap.Sessions.Target {
			t.Fatalf("generation %d sync requests = %d, want %d", generation, len(requests), bootstrap.Sessions.Target)
		}
		for _, request := range requests {
			if request != NewConversationSyncRequest(request.UID) {
				t.Fatalf("generation %d did not issue a version-zero full sync: %+v", generation, request)
			}
		}

		// The next tick may either observe asynchronous login completions or
		// release traffic when the first step already observed all completions.
		// Both schedules start allocator credit only after the local target.
		clock.Set(startedAt.Add(46*time.Minute + time.Second))
		ticker.tickAndWait(t)
		traffic, err := worker.Checkpoint(context.Background())
		if err != nil {
			t.Fatalf("observed bootstrap checkpoint generation %d: %v", generation, err)
		}
		if traffic.Sessions.CompletedNew+traffic.Sessions.CompletedReturning < uint64(traffic.Sessions.Target) {
			t.Fatalf("generation %d bootstrap observation = %+v", generation, traffic)
		}
		if traffic.Generated.Primary == 0 {
			clock.Set(startedAt.Add(46*time.Minute + 31*time.Second))
			ticker.tickAndWait(t)
			traffic, err = worker.Checkpoint(context.Background())
			if err != nil {
				t.Fatalf("traffic checkpoint generation %d: %v", generation, err)
			}
		}
		localBurst, targetErr := workerOnlineTarget(config.Workload.MaxGlobalBurst, 0, uint64(config.Workload.Workers))
		if targetErr != nil {
			t.Fatalf("generation %d local burst: %v", generation, targetErr)
		}
		if traffic.Generated.Primary == 0 || traffic.Generated.Primary > uint64(localBurst) {
			t.Fatalf("generation %d primary = %d, want 1..%d", generation, traffic.Generated.Primary, localBurst)
		}
		packets := sessions.sentPackets()
		if len(packets) == 0 {
			t.Fatalf("generation %d released primary traffic without a WKProto SEND", generation)
		}

		if exerciseChurn {
			clock.Set(startedAt.Add(4 * time.Hour))
			ticker.tickAndWait(t)
			churn, checkpointErr := worker.Checkpoint(context.Background())
			if checkpointErr != nil {
				t.Fatalf("churn checkpoint: %v", checkpointErr)
			}
			if !workerEvidenceHasCode(churn.Evidence, FailureCodeOfferedLoadUnderDelivery) {
				t.Fatalf("traffic stopped again after churn instead of recording classified evidence: %+v", churn)
			}
		}
		select {
		case doneErr := <-worker.Done():
			t.Fatalf("generation %d terminated on classified workload evidence: %v", generation, doneErr)
		default:
		}
		return packets[0].ClientMsgNo
	}

	identity7 := run(7, true)
	identity8 := run(8, false)
	if identity7 == identity8 {
		t.Fatalf("assigned generation did not scope client_msg_no: %q", identity7)
	}
}

func TestWorkerEngineCoordinatorModeEmitsPrimaryOnlyFromExternalGrant(t *testing.T) {
	config := LocalConfig()
	config.RunID = "worker-external-grant"
	config.Workload.OnlineUsers = 12
	config.Workload.SendRatePerSecond = 12
	config.Workload.MaxGlobalBurst = 24
	config.Workload.Sessions = []DurationShare{{Percent: 100, Min: 3 * time.Hour, Max: 3 * time.Hour}}
	if err := config.Validate(); err != nil {
		t.Fatalf("external grant config: %v", err)
	}
	startedAt := time.Unix(1_700_000_000, 0)
	clock := &sessionFakeClock{now: startedAt}
	sessions := &engineFakeFactory{}
	ticker := newManualWorkerGenerationTicker()
	factory := engineWorkerGenerationFactory{
		clock:             clock,
		newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) { return sessions, nil },
		newSyncer: func(WorkerAssignment) (ConversationSyncer, error) {
			return &workerBootstrapSyncer{blockAfter: 4}, nil
		},
		newTicker: func(time.Duration) workerGenerationTicker { return ticker },
	}
	worker, err := factory.New(WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "external-grant", Generation: 11},
		WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), CoordinatorGrants: true, Config: config,
	})
	if err != nil {
		t.Fatalf("New external-grant generation: %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start external-grant generation: %v", err)
	}
	defer worker.Stop()
	ticker.awaitReady(t)

	clock.Set(startedAt.Add(46 * time.Minute))
	ticker.tickAndWait(t)
	worker.(*engineWorkerGeneration).engine.loginOps.Wait()
	for tick := 0; tick < 3 && !worker.TrafficReady(); tick++ {
		clock.Set(clock.Now().Add(time.Second))
		ticker.tickAndWait(t)
	}
	if !worker.TrafficReady() {
		t.Fatal("worker did not publish traffic readiness after synchronized bootstrap")
	}
	before, err := worker.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("pre-grant checkpoint: %v", err)
	}
	if before.Generated.Primary != 0 {
		t.Fatalf("coordinator mode autonomous primary = %d, want 0", before.Generated.Primary)
	}

	clock.Set(clock.Now().Add(time.Second))
	ticker.tickAndWait(t)
	autonomous, err := worker.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("autonomous checkpoint: %v", err)
	}
	if autonomous.Generated.Primary != 0 {
		t.Fatalf("coordinator mode local allocator released %d primary messages", autonomous.Generated.Primary)
	}
	if application, err := worker.ApplyGrant(context.Background(), 4); err != nil || !application.Admitted {
		t.Fatalf("ApplyGrant: %v", err)
	}
	after, err := worker.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("post-grant checkpoint: %v", err)
	}
	if after.Generated.Primary != 4 {
		t.Fatalf("external primary release = %d, want exactly 4", after.Generated.Primary)
	}
}

func TestWorkerEngineGenerationRejectsOverflowAndReportsClockRollbackAsFatalRuntimeTermination(t *testing.T) {
	config := LocalConfig()
	assignment := WorkerAssignment{
		WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "fatal", Generation: maxLogicalGeneration + 1},
		WorkerID:    0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	}
	if generation, err := NewEngineWorkerGenerationFactory().New(assignment); !errors.Is(err, errWorkerServerConfig) || generation != nil {
		t.Fatalf("overflow generation = %#v, %v; want nil, %v", generation, err, errWorkerServerConfig)
	}

	startedAt := time.Unix(1_700_000_000, 0)
	clock := &sessionFakeClock{now: startedAt}
	ticker := newManualWorkerGenerationTicker()
	factory := engineWorkerGenerationFactory{
		clock: clock,
		newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) {
			return &engineFakeFactory{}, nil
		},
		newSyncer: func(WorkerAssignment) (ConversationSyncer, error) {
			return engineSyncer{}, nil
		},
		newTicker: func(time.Duration) workerGenerationTicker { return ticker },
	}
	assignment.Generation = 9
	generation, err := factory.New(assignment)
	if err != nil {
		t.Fatalf("New fatal generation: %v", err)
	}
	if err := generation.Start(context.Background()); err != nil {
		t.Fatalf("Start fatal generation: %v", err)
	}
	ticker.awaitReady(t)
	clock.Set(startedAt.Add(-time.Second))
	ticker.tick(t)
	select {
	case doneErr := <-generation.Done():
		assertClockRollbackFailure(t, doneErr)
	case <-time.After(time.Second):
		generation.Stop()
		t.Fatal("fatal runtime termination did not signal Done")
	}
	snapshot, snapshotErr := generation.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatalf("fatal Snapshot: %v", snapshotErr)
	}
	if snapshot.Generation != 9 {
		t.Fatalf("fatal generation snapshot = %+v", snapshot)
	}
}

type workerBootstrapSyncer struct {
	mu         sync.Mutex
	requestsV  []target.ConversationSyncRequest
	blockAfter int
}

func (s *workerBootstrapSyncer) ConversationSync(ctx context.Context, request target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	s.mu.Lock()
	s.requestsV = append(s.requestsV, request)
	count := len(s.requestsV)
	s.mu.Unlock()
	if count > s.blockAfter {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	return nil, nil
}

func (s *workerBootstrapSyncer) requests() []target.ConversationSyncRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]target.ConversationSyncRequest(nil), s.requestsV...)
}

type manualWorkerGenerationTicker struct {
	ready chan struct{}
	ticks chan struct{}
}

func newManualWorkerGenerationTicker() *manualWorkerGenerationTicker {
	return &manualWorkerGenerationTicker{ready: make(chan struct{}, 1), ticks: make(chan struct{})}
}

func (t *manualWorkerGenerationTicker) Wait(ctx context.Context) bool {
	select {
	case t.ready <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	select {
	case <-t.ticks:
		return true
	case <-ctx.Done():
		return false
	}
}

func (*manualWorkerGenerationTicker) Stop() {}

func (t *manualWorkerGenerationTicker) awaitReady(testingT *testing.T) {
	testingT.Helper()
	select {
	case <-t.ready:
	case <-time.After(time.Second):
		testingT.Fatal("worker ticker did not reach its deterministic wait boundary")
	}
}

func (t *manualWorkerGenerationTicker) tickAndWait(testingT *testing.T) {
	testingT.Helper()
	t.tick(testingT)
	t.awaitReady(testingT)
}

func (t *manualWorkerGenerationTicker) tick(testingT *testing.T) {
	testingT.Helper()
	select {
	case t.ticks <- struct{}{}:
	case <-time.After(time.Second):
		testingT.Fatal("worker ticker did not accept a deterministic tick")
	}
}

func workerEvidenceHasCode(snapshot EvidenceSnapshot, code FailureCode) bool {
	for _, class := range snapshot.Classes {
		for _, example := range append(append([]EvidenceExample(nil), class.First...), class.Last...) {
			if example.Code == code {
				return true
			}
		}
	}
	return false
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

func TestWorkerServerSnapshotsCarryExactFenceAndStableFinalSequence(t *testing.T) {
	generation := newFakeWorkerGeneration()
	now := time.Unix(1_700_000_000, 0)
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return generation, nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWorkerServer() error = %v", err)
	}
	cfg := LocalConfig()
	cfg.RunID = "worker-snapshot-fence"
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "worker-snapshot-assignment", Generation: 11}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 1, WorkerCount: coordinatorWorkerCount, Config: cfg,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})

	now = now.Add(time.Second)
	first := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil))
	if first.RunID != fence.RunID || first.AssignmentID != fence.AssignmentID || first.Generation != fence.Generation ||
		first.WorkerID != 1 || first.SnapshotSequence != 1 {
		t.Fatalf("first snapshot fence/sequence = %+v", first)
	}
	now = now.Add(time.Second)
	second := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/checkpoint", WorkerCheckpointRequest{WorkerFence: fence}))
	if second.SnapshotSequence != 2 || second.Uptime <= first.Uptime {
		t.Fatalf("second snapshot sequence/uptime = %d/%s, first = %d/%s", second.SnapshotSequence, second.Uptime, first.SnapshotSequence, first.Uptime)
	}

	now = now.Add(time.Second)
	final := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence}))
	retry := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence}))
	if final.Phase != WorkerPhaseFinal || final.SnapshotSequence != 3 || retry.SnapshotSequence != final.SnapshotSequence {
		t.Fatalf("final/retry phase and sequence = %s/%d and %s/%d", final.Phase, final.SnapshotSequence, retry.Phase, retry.SnapshotSequence)
	}
}

func decodeWorkerSnapshotResponse(t *testing.T, response *httptest.ResponseRecorder) WorkerSnapshot {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d, want %d; body = %q", response.Code, http.StatusOK, response.Body.String())
	}
	var snapshot WorkerSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snapshot
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
	// Stop entering the generation is not the publication boundary for the
	// server's final snapshot. A retry on the same fence joins the existing
	// stop task and therefore deterministically observes that boundary.
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})

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

func TestWorkerServerStopCleansAssignedGenerationAndAllowsHigherFence(t *testing.T) {
	for _, test := range []struct {
		name      string
		startFail bool
	}{
		{name: "not started"},
		{name: "start failed", startFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var generations []*fakeWorkerGeneration
			server, err := NewWorkerServer(WorkerServerConfig{
				ControlToken: "control-secret",
				Factory: WorkerGenerationFactoryFunc(func(assignment WorkerAssignment) (WorkerGeneration, error) {
					generation := newFakeWorkerGeneration()
					generation.snapshot.Generation = assignment.Generation
					if len(generations) == 0 && test.startFail {
						generation.startErr = errors.New("redacted start failure")
					}
					generations = append(generations, generation)
					return generation, nil
				}),
			})
			if err != nil {
				t.Fatalf("NewWorkerServer: %v", err)
			}
			config := LocalConfig()
			fence7 := WorkerFence{RunID: config.RunID, AssignmentID: "assigned-stop", Generation: 7}
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
				WorkerFence: fence7, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
			})
			if test.startFail {
				assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence7},
					http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
			}

			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence7})
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence7})
			if generations[0].drains != 0 || generations[0].stops != 1 {
				t.Fatalf("assigned cleanup drain/stop = %d/%d, want 0/1", generations[0].drains, generations[0].stops)
			}

			fence8 := WorkerFence{RunID: config.RunID, AssignmentID: "assigned-stop-next", Generation: 8}
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
				WorkerFence: fence8, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
			})
			assertWorkerError(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence7},
				http.StatusConflict, WorkerErrorFenceMismatch)
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence8})
			if len(generations) != 2 || generations[1].drains != 0 || generations[1].stops != 1 {
				t.Fatalf("higher generation cleanup = %#v", generations)
			}
		})
	}
}

func TestWorkerServerCanceledBlockingControlsDoNotHoldLifecycleLock(t *testing.T) {
	for _, operation := range []string{"snapshot", "checkpoint", "rate"} {
		t.Run(operation, func(t *testing.T) {
			generation := newBlockingControlGeneration(operation)
			server, fence := startWorkerServerForGeneration(t, generation, "blocking-"+operation)

			requestContext, cancelRequest := context.WithCancel(context.Background())
			var method, path string
			var body any
			if operation == "snapshot" {
				method, path = http.MethodGet, "/v1/chat-lifecycle/snapshot"
			} else if operation == "checkpoint" {
				method, path = http.MethodPost, "/v1/chat-lifecycle/checkpoint"
				body = WorkerCheckpointRequest{WorkerFence: fence}
			} else {
				method, path = http.MethodPost, "/v1/chat-lifecycle/rate"
				body = WorkerRateRequest{WorkerFence: fence, RatePerSecond: 120, MaxBurst: 240}
			}
			controlDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				controlDone <- workerRequestWithContext(t, server, requestContext, method, path, body)
			}()
			select {
			case <-generation.controlStarted:
			case <-time.After(time.Second):
				t.Fatal("blocking control did not start")
			}

			statusDone := make(chan int, 1)
			healthDone := make(chan int, 1)
			go func() { statusDone <- workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/status", nil).Code }()
			go func() { healthDone <- workerRequest(t, server, http.MethodGet, "/healthz", nil).Code }()
			statusQuick := receivesWorkerStatus(statusDone)
			healthQuick := receivesWorkerStatus(healthDone)

			cancelRequest()
			stopDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				stopDone <- workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence})
			}()
			stopStarted := false
			select {
			case <-generation.drainStarted:
				stopStarted = true
			case <-time.After(100 * time.Millisecond):
			}

			close(generation.controlRelease)
			<-controlDone
			stopResponse := <-stopDone
			if !statusQuick || !healthQuick {
				t.Fatalf("status/health blocked behind %s: quick=%v/%v", operation, statusQuick, healthQuick)
			}
			if !stopStarted {
				t.Fatalf("explicit stop could not start after canceled %s", operation)
			}
			if stopResponse.Code != http.StatusOK {
				t.Fatalf("stop response = %d/%q", stopResponse.Code, stopResponse.Body.String())
			}
		})
	}
}

func TestWorkerServerDiscardsOldSnapshotResultAfterHigherGenerationAssignment(t *testing.T) {
	oldGeneration := newBlockingControlGeneration("snapshot")
	newGeneration := newFakeWorkerGeneration()
	created := 0
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(assignment WorkerAssignment) (WorkerGeneration, error) {
			created++
			if created == 1 {
				oldGeneration.snapshot.Generation = assignment.Generation
				oldGeneration.snapshot.Messages.Sent = 71
				return oldGeneration, nil
			}
			newGeneration.snapshot.Generation = assignment.Generation
			newGeneration.snapshot.Messages.Sent = 82
			return newGeneration, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewWorkerServer: %v", err)
	}
	config := LocalConfig()
	fence7 := WorkerFence{RunID: config.RunID, AssignmentID: "old-snapshot", Generation: 7}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence7, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence7})

	oldResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() { oldResponse <- workerRequest(t, server, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil) }()
	<-oldGeneration.controlStarted

	// Stop takes its own unblocked final snapshot after Stop, then generation 8
	// is assigned before the old request is allowed to return.
	stopResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		stopResponse <- workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence7})
	}()
	select {
	case response := <-stopResponse:
		if response.Code != http.StatusOK {
			t.Fatalf("stop response = %d/%q", response.Code, response.Body.String())
		}
	case <-time.After(100 * time.Millisecond):
		close(oldGeneration.controlRelease)
		<-stopResponse
		<-oldResponse
		t.Fatal("stop blocked behind old snapshot")
	}
	fence8 := WorkerFence{RunID: config.RunID, AssignmentID: "new-snapshot", Generation: 8}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence8, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	close(oldGeneration.controlRelease)
	response := <-oldResponse
	if response.Code == http.StatusOK {
		var snapshot WorkerSnapshot
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("decode stale snapshot: %v", err)
		}
		if snapshot.Generation == 8 && snapshot.Messages.Sent == 71 {
			t.Fatalf("old snapshot was overlaid onto new fence: %+v", snapshot)
		}
	}
}

func TestWorkerServerRejectsLateControlErrorsAfterHigherGenerationAssignment(t *testing.T) {
	for _, operation := range []string{"snapshot", "checkpoint", "rate"} {
		t.Run(operation, func(t *testing.T) {
			oldGeneration := newBlockingControlGeneration(operation)
			oldGeneration.controlErr = errors.New("old-generation-secret-" + operation)
			newGeneration := newFakeWorkerGeneration()
			created := 0
			server, err := NewWorkerServer(WorkerServerConfig{
				ControlToken: "control-secret",
				Factory: WorkerGenerationFactoryFunc(func(assignment WorkerAssignment) (WorkerGeneration, error) {
					created++
					if created == 1 {
						oldGeneration.snapshot.Generation = assignment.Generation
						return oldGeneration, nil
					}
					newGeneration.snapshot.Generation = assignment.Generation
					return newGeneration, nil
				}),
			})
			if err != nil {
				t.Fatalf("NewWorkerServer: %v", err)
			}
			config := LocalConfig()
			fence7 := WorkerFence{RunID: config.RunID, AssignmentID: "late-error-" + operation, Generation: 7}
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
				WorkerFence: fence7, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
			})
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence7})

			method, path, body := http.MethodGet, "/v1/chat-lifecycle/snapshot", any(nil)
			switch operation {
			case "checkpoint":
				method, path = http.MethodPost, "/v1/chat-lifecycle/checkpoint"
				body = WorkerCheckpointRequest{WorkerFence: fence7}
			case "rate":
				method, path = http.MethodPost, "/v1/chat-lifecycle/rate"
				body = WorkerRateRequest{WorkerFence: fence7, RatePerSecond: 120, MaxBurst: 240}
			}
			oldResponse := make(chan *httptest.ResponseRecorder, 1)
			go func() { oldResponse <- workerRequest(t, server, method, path, body) }()
			<-oldGeneration.controlStarted

			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/stop", WorkerStopRequest{WorkerFence: fence7})
			fence8 := WorkerFence{RunID: config.RunID, AssignmentID: "late-error-next-" + operation, Generation: 8}
			assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
				WorkerFence: fence8, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
			})
			close(oldGeneration.controlRelease)
			response := <-oldResponse
			if response.Code != http.StatusConflict {
				t.Fatalf("late %s error status = %d, want %d; body=%q", operation, response.Code, http.StatusConflict, response.Body.String())
			}
			var apiError WorkerAPIError
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode late %s error: %v", operation, err)
			}
			if apiError.Code != WorkerErrorFenceMismatch {
				t.Fatalf("late %s error code = %q, want %q", operation, apiError.Code, WorkerErrorFenceMismatch)
			}
			if strings.Contains(response.Body.String(), "old-generation-secret") || apiError.Code == WorkerErrorRuntimeFailure {
				t.Fatalf("late %s leaked stale runtime error: %q", operation, response.Body.String())
			}
		})
	}
}

type blockingControlGeneration struct {
	*fakeWorkerGeneration
	operation      string
	controlStarted chan struct{}
	controlRelease chan struct{}
	blockOnce      atomic.Bool
	startedOnce    sync.Once
	controlErr     error
}

func newBlockingControlGeneration(operation string) *blockingControlGeneration {
	generation := &blockingControlGeneration{
		fakeWorkerGeneration: newFakeWorkerGeneration(),
		operation:            operation,
		controlStarted:       make(chan struct{}),
		controlRelease:       make(chan struct{}),
	}
	generation.drainStarted = make(chan struct{})
	generation.blockOnce.Store(true)
	return generation
}

func (g *blockingControlGeneration) Checkpoint(ctx context.Context) (WorkerSnapshot, error) {
	if g.operation == "checkpoint" {
		blocked, err := g.blockControl(ctx)
		if err != nil {
			return WorkerSnapshot{}, err
		}
		if blocked && g.controlErr != nil {
			return WorkerSnapshot{}, g.controlErr
		}
	}
	return g.fakeWorkerGeneration.Checkpoint(ctx)
}

func (g *blockingControlGeneration) UpdateRate(ctx context.Context, ratePerSecond, maxBurst uint64) error {
	if g.operation == "rate" {
		blocked, err := g.blockControl(ctx)
		if err != nil {
			return err
		}
		if blocked && g.controlErr != nil {
			return g.controlErr
		}
	}
	return g.fakeWorkerGeneration.UpdateRate(ctx, ratePerSecond, maxBurst)
}

func (g *blockingControlGeneration) Snapshot(ctx context.Context) (WorkerSnapshot, error) {
	if g.operation == "snapshot" {
		blocked, err := g.blockControl(ctx)
		if err != nil {
			return WorkerSnapshot{}, err
		}
		if blocked && g.controlErr != nil {
			return WorkerSnapshot{}, g.controlErr
		}
	}
	return g.fakeWorkerGeneration.Snapshot(ctx)
}

func (g *blockingControlGeneration) blockControl(ctx context.Context) (bool, error) {
	if !g.blockOnce.CompareAndSwap(true, false) {
		return false, nil
	}
	g.startedOnce.Do(func() { close(g.controlStarted) })
	select {
	case <-g.controlRelease:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

func receivesWorkerStatus(result <-chan int) bool {
	select {
	case status := <-result:
		return status == http.StatusOK
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func startWorkerServerForGeneration(t *testing.T, generation WorkerGeneration, assignmentID string) (*WorkerServer, WorkerFence) {
	t.Helper()
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewWorkerServer: %v", err)
	}
	config := LocalConfig()
	fence := WorkerFence{RunID: config.RunID, AssignmentID: assignmentID, Generation: 7}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})
	return server, fence
}

func startWorkerServerForCoordinatorGeneration(t *testing.T, generation WorkerGeneration, assignmentID string) (*WorkerServer, WorkerFence) {
	t.Helper()
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret",
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
			return generation, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewWorkerServer: %v", err)
	}
	config := LocalConfig()
	fence := WorkerFence{RunID: config.RunID, AssignmentID: assignmentID, Generation: 7}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), CoordinatorGrants: true, Config: config,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})
	return server, fence
}

type fakeWorkerGeneration struct {
	startErr     error
	starts       int
	rate         uint64
	burst        uint64
	grants       []uint64
	grantErr     error
	checkpoints  int
	drains       int
	stops        int
	snapshot     WorkerSnapshot
	done         chan error
	drainStarted chan struct{}
	drainRelease chan struct{}
	stopped      chan struct{}
	drainErr     error
	snapshotErr  error
	doneOnce     sync.Once
}

type fakeLifecycleLeaseGeneration struct {
	*fakeWorkerGeneration
	candidates                     []LifecycleCandidate
	requested                      int
	approved                       string
	approvedToken, approvedVersion uint64
	approveResult                  *bool
	approveEntered, approveRelease chan struct{}
	approveCompleted               bool
}

func (g *fakeLifecycleLeaseGeneration) ApproveLifecycleReheat(ctx context.Context, identity string, timerToken, activityVersion uint64) (bool, error) {
	return g.approveLifecycleReheat(ctx, identity, timerToken, activityVersion)
}

func (g *fakeLifecycleLeaseGeneration) approveLifecycleReheat(ctx context.Context, identity string, timerToken, activityVersion uint64) (bool, error) {
	if g.approveEntered != nil {
		close(g.approveEntered)
		select {
		case <-g.approveRelease:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	g.approved = identity
	g.approvedToken = timerToken
	g.approvedVersion = activityVersion
	g.approveCompleted = true
	if g.approveResult != nil {
		return *g.approveResult, nil
	}
	return true, nil
}

func (g *fakeLifecycleLeaseGeneration) LeaseLifecycleCandidates(_ context.Context, requested int) ([]LifecycleCandidate, error) {
	g.requested = requested
	return append([]LifecycleCandidate(nil), g.candidates...), nil
}

type cancelBeforeGrantAdmissionGeneration struct {
	*fakeWorkerGeneration
	firstEntered chan struct{}
	calls        int
}

func (g *cancelBeforeGrantAdmissionGeneration) ApplyGrant(ctx context.Context, released uint64) (WorkerGrantApplication, error) {
	g.calls++
	if g.calls == 1 {
		close(g.firstEntered)
		<-ctx.Done()
		return WorkerGrantApplication{}, ctx.Err()
	}
	g.grants = append(g.grants, released)
	return WorkerGrantApplication{Admitted: true}, nil
}

type blockingStartWorkerGeneration struct {
	*fakeWorkerGeneration
	startEntered chan struct{}
	startRelease chan struct{}
}

func (g *blockingStartWorkerGeneration) Start(context.Context) error {
	g.starts++
	close(g.startEntered)
	<-g.startRelease
	return nil
}

func newFakeWorkerGeneration() *fakeWorkerGeneration {
	return &fakeWorkerGeneration{done: make(chan error, 1)}
}

func (g *fakeWorkerGeneration) Start(context.Context) error {
	g.starts++
	return g.startErr
}

func (g *fakeWorkerGeneration) UpdateRate(_ context.Context, ratePerSecond, maxBurst uint64) error {
	g.rate, g.burst = ratePerSecond, maxBurst
	return nil
}

func (g *fakeWorkerGeneration) ApplyGrant(_ context.Context, released uint64) (WorkerGrantApplication, error) {
	g.grants = append(g.grants, released)
	return WorkerGrantApplication{Admitted: true}, g.grantErr
}

func (g *fakeWorkerGeneration) TrafficReady() bool { return true }

func (g *fakeWorkerGeneration) Checkpoint(context.Context) (WorkerSnapshot, error) {
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

func (g *fakeWorkerGeneration) Snapshot(context.Context) (WorkerSnapshot, error) {
	return g.snapshot, g.snapshotErr
}

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
	return workerRequestWithContext(t, server, context.Background(), method, path, body)
}

func workerRequestWithContext(t *testing.T, server http.Handler, ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded strings.Builder
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(encoded.String())).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	return response
}
