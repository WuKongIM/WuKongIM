package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkerClientSendsAuthenticatedTypedRequestsAndRejectsUnknownResponses(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer control-secret" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat-lifecycle/start" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"phase":"running","generation":3,"worker_id":1,"worker_count":3,"unexpected":false}`))
	}))
	defer server.Close()

	client, err := NewWorkerClient(WorkerClientConfig{
		BaseURL:      server.URL,
		ControlToken: "control-secret",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("new worker client: %v", err)
	}
	status, err := client.Start(context.Background(), WorkerStartRequest{WorkerFence: WorkerFence{
		RunID: "run", AssignmentID: "assignment", Generation: 3,
	}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if status.Phase != WorkerPhaseRunning || status.Generation != 3 || requests != 1 {
		t.Fatalf("status/requests = %+v/%d", status, requests)
	}

	unknownServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"ok":true,"phase":"running","raw_uid":"must-not-pass"}`))
	}))
	defer unknownServer.Close()
	unknownClient, err := NewWorkerClient(WorkerClientConfig{BaseURL: unknownServer.URL, ControlToken: "control-secret", HTTPClient: unknownServer.Client()})
	if err != nil {
		t.Fatalf("new unknown-field client: %v", err)
	}
	if _, err := unknownClient.Health(context.Background()); !errors.Is(err, ErrWorkerResponse) {
		t.Fatalf("health error = %v, want ErrWorkerResponse", err)
	}
}

func TestWorkerClientReturnsStructuredAPIErrorAndHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/chat-lifecycle/rate" {
			response.WriteHeader(http.StatusConflict)
			_, _ = response.Write([]byte(`{"code":"invalid_state"}`))
			return
		}
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := NewWorkerClient(WorkerClientConfig{BaseURL: server.URL, ControlToken: "control-secret", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("new worker client: %v", err)
	}

	_, err = client.UpdateRate(context.Background(), WorkerRateRequest{
		WorkerFence:   WorkerFence{RunID: "run", AssignmentID: "assignment", Generation: 1},
		RatePerSecond: 100, MaxBurst: 200,
	})
	var apiError *WorkerAPIError
	if !errors.As(err, &apiError) || apiError.Code != WorkerErrorInvalidState || apiError.Status != http.StatusConflict {
		t.Fatalf("rate error = %#v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("status error = %v, want context canceled", err)
	}
}

func TestWorkerClientSendsAndStrictlyDecodesCoordinatorGrant(t *testing.T) {
	t.Parallel()
	fence := WorkerFence{RunID: "grant-run", AssignmentID: "grant-assignment", Generation: 4}
	grant := WorkerGrantRequest{
		WorkerFence: fence, Sequence: 9, RatePerSecond: 120, MaxBurst: 240,
		Fresh:    WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
		Released: WorkerGrantCounts{Worker0: 40, Worker1: 40, Worker2: 40},
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat-lifecycle/grant" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var observed WorkerGrantRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&observed); err != nil || observed != grant {
			t.Fatalf("grant request = %+v, %v; want %+v", observed, err, grant)
		}
		_ = json.NewEncoder(response).Encode(WorkerGrantResponse{
			WorkerFence: fence, WorkerID: 1, WorkerCount: 3, Sequence: 9, Released: 40,
		})
	}))
	defer server.Close()
	client, err := NewWorkerClient(WorkerClientConfig{
		BaseURL: server.URL, ControlToken: "control-secret", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewWorkerClient: %v", err)
	}
	response, err := client.Grant(context.Background(), grant)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if response.Sequence != grant.Sequence || response.Released != 40 {
		t.Fatalf("grant response = %+v", response)
	}

	unknownServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"run_id":"grant-run","assignment_id":"grant-assignment","generation":4,"worker_id":1,"worker_count":3,"sequence":9,"released":40,"raw_uid":"forbidden"}`))
	}))
	defer unknownServer.Close()
	unknownClient, err := NewWorkerClient(WorkerClientConfig{
		BaseURL: unknownServer.URL, ControlToken: "control-secret", HTTPClient: unknownServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewWorkerClient unknown: %v", err)
	}
	if _, err := unknownClient.Grant(context.Background(), grant); !errors.Is(err, ErrWorkerResponse) {
		t.Fatalf("unknown grant response error = %v, want ErrWorkerResponse", err)
	}
}

func TestWorkerClientRejectsOversizedAndUnboundedSnapshotResponses(t *testing.T) {
	t.Parallel()

	oversizedServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"ok":true,"padding":"` + string(make([]byte, 64)) + `"}`))
	}))
	defer oversizedServer.Close()
	oversizedClient, err := NewWorkerClient(WorkerClientConfig{
		BaseURL: oversizedServer.URL, ControlToken: "control-secret", HTTPClient: oversizedServer.Client(), MaxResponseBytes: 32,
	})
	if err != nil {
		t.Fatalf("new oversized client: %v", err)
	}
	if _, err := oversizedClient.Health(context.Background()); !errors.Is(err, ErrWorkerResponse) {
		t.Fatalf("oversized health error = %v, want ErrWorkerResponse", err)
	}

	unboundedServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(WorkerSnapshot{Evidence: EvidenceSnapshot{Classes: []EvidenceClassSnapshot{{
			Class: FailureClassHarness, First: make([]EvidenceExample, maxEvidenceExamplesPerSide+1),
		}}}})
	}))
	defer unboundedServer.Close()
	unboundedClient, err := NewWorkerClient(WorkerClientConfig{BaseURL: unboundedServer.URL, ControlToken: "control-secret", HTTPClient: unboundedServer.Client()})
	if err != nil {
		t.Fatalf("new unbounded client: %v", err)
	}
	if _, err := unboundedClient.Snapshot(context.Background()); !errors.Is(err, ErrWorkerResponse) {
		t.Fatalf("unbounded snapshot error = %v, want ErrWorkerResponse", err)
	}
}
