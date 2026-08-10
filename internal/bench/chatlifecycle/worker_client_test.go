package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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

func TestWorkerClientLifecycleCandidateLeaseRejectsDuplicateOversizeAndFenceMutation(t *testing.T) {
	fence := WorkerFence{RunID: "run", AssignmentID: "assignment", Generation: 1}
	candidate := lifecycleTestCandidates(t, time.Unix(1_000, 0))[0]
	for _, test := range []struct {
		name     string
		response WorkerLifecycleCandidateLeaseResponse
	}{
		{"duplicate", WorkerLifecycleCandidateLeaseResponse{WorkerFence: fence, WorkerID: 0, WorkerCount: 3, Candidates: []LifecycleCandidate{candidate, candidate}}},
		{"over requested", WorkerLifecycleCandidateLeaseResponse{WorkerFence: fence, WorkerID: 0, WorkerCount: 3, Candidates: []LifecycleCandidate{candidate, candidate}}},
		{"wrong fence", WorkerLifecycleCandidateLeaseResponse{WorkerFence: WorkerFence{RunID: "other", AssignmentID: "assignment", Generation: 1}, WorkerID: 0, WorkerCount: 3, Candidates: []LifecycleCandidate{candidate}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(test.response) }))
			defer server.Close()
			client, err := NewWorkerClient(WorkerClientConfig{BaseURL: server.URL, ControlToken: "token", HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			requested := uint16(1)
			if test.name == "duplicate" {
				requested = 2
			}
			if _, err := client.LeaseLifecycleCandidates(context.Background(), WorkerLifecycleCandidateLeaseRequest{WorkerFence: fence, Requested: requested}); !errors.Is(err, ErrWorkerResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	t.Run("reheat response cannot echo raw identity", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": fence.RunID, "assignment_id": fence.AssignmentID, "generation": fence.Generation, "worker_id": 0, "worker_count": 3, "approved": true, "channel_id": candidate.ChannelID, "timer_token": candidate.TimerToken})
		}))
		defer server.Close()
		client, err := NewWorkerClient(WorkerClientConfig{BaseURL: server.URL, ControlToken: "token", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ApproveLifecycleReheat(context.Background(), WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken, ActivityVersion: candidate.ActivityVersion}); !errors.Is(err, ErrWorkerResponse) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reheat request requires exact lease fence", func(t *testing.T) {
		client, err := NewWorkerClient(WorkerClientConfig{BaseURL: "http://127.0.0.1:1", ControlToken: "token"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ApproveLifecycleReheat(context.Background(), WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, ActivityVersion: candidate.ActivityVersion}); !errors.Is(err, ErrWorkerClientConfig) {
			t.Fatalf("zero token error = %v", err)
		}
		if _, err := client.ApproveLifecycleReheat(context.Background(), WorkerLifecycleReheatRequest{WorkerFence: fence, ChannelID: candidate.ChannelID, TimerToken: candidate.TimerToken}); !errors.Is(err, ErrWorkerClientConfig) {
			t.Fatalf("zero activity version error = %v", err)
		}
	})
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

func TestValidWorkerAPIErrorRequiresClosedRuntimeCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value WorkerAPIError
		want  bool
	}{
		{name: "ordinary closed error", value: WorkerAPIError{Code: WorkerErrorInvalidState}, want: true},
		{name: "legacy generic runtime failure", value: WorkerAPIError{Code: WorkerErrorRuntimeFailure}, want: true},
		{name: "classified runtime failure", value: WorkerAPIError{Code: WorkerErrorRuntimeFailure, RuntimeCode: RuntimeFailureEngineCPUSaturated}, want: true},
		{name: "runtime code on non-runtime error", value: WorkerAPIError{Code: WorkerErrorInvalidState, RuntimeCode: RuntimeFailureEngineCPUSaturated}},
		{name: "unknown runtime code", value: WorkerAPIError{Code: WorkerErrorRuntimeFailure, RuntimeCode: RuntimeFailureCode("unknown")}},
		{name: "unknown worker error", value: WorkerAPIError{Code: WorkerErrorCode("unknown")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validWorkerAPIError(test.value); got != test.want {
				t.Fatalf("validWorkerAPIError(%+v) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestWorkerClientClassifiesTransportCancellationCausally(t *testing.T) {
	t.Run("ordinary request error survives synchronous late cancel", func(t *testing.T) {
		ordinaryErr := errors.New("injected ordinary transport error")
		ctx, cancel := context.WithCancel(context.Background())
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				cancel()
				return nil, ordinaryErr
			})},
		})
		if err != nil {
			t.Fatalf("NewWorkerClient() error = %v", err)
		}
		if _, err := client.Status(ctx); !errors.Is(err, ordinaryErr) || errors.Is(err, context.Canceled) {
			t.Fatalf("Status() error = %v, want ordinary transport error", err)
		}
	})

	t.Run("causal request cancellation returns context canceled", func(t *testing.T) {
		entered := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				close(entered)
				<-request.Context().Done()
				return nil, request.Context().Err()
			})},
		})
		if err != nil {
			t.Fatalf("NewWorkerClient() error = %v", err)
		}
		result := make(chan error, 1)
		go func() {
			_, requestErr := client.Status(ctx)
			result <- requestErr
		}()
		<-entered
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Status() error = %v, want context canceled", err)
		}
	})

	t.Run("causal body cancellation after headers returns context canceled", func(t *testing.T) {
		bodyEntered := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       &cancelingWorkerResponseBody{ctx: request.Context(), entered: bodyEntered},
				}, nil
			})},
		})
		if err != nil {
			t.Fatalf("NewWorkerClient() error = %v", err)
		}
		result := make(chan error, 1)
		go func() {
			_, requestErr := client.Status(ctx)
			result <- requestErr
		}()
		<-bodyEntered
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Status() body error = %v, want context canceled", err)
		}
	})

	t.Run("ordinary body error keeps stable response classification", func(t *testing.T) {
		ordinaryErr := errors.New("injected ordinary body error")
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: &errorWorkerResponseBody{err: ordinaryErr},
				}, nil
			})},
		})
		if err != nil {
			t.Fatalf("NewWorkerClient() error = %v", err)
		}
		if _, err := client.Status(context.Background()); !errors.Is(err, ErrWorkerResponse) || errors.Is(err, ordinaryErr) {
			t.Fatalf("Status() body error = %v, want stable ErrWorkerResponse", err)
		}
	})

	t.Run("ordinary body error survives synchronous late cancel", func(t *testing.T) {
		ordinaryErr := errors.New("injected ordinary body error before late cancel")
		ctx, cancel := context.WithCancel(context.Background())
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: &cancelThenErrorWorkerResponseBody{cancel: cancel, err: ordinaryErr},
				}, nil
			})},
		})
		if err != nil {
			t.Fatalf("NewWorkerClient() error = %v", err)
		}
		if _, err := client.Status(ctx); !errors.Is(err, ErrWorkerResponse) || errors.Is(err, context.Canceled) {
			t.Fatalf("Status() body error = %v, want ErrWorkerResponse without context cancellation", err)
		}
	})
}

func TestWorkerClientOrdinaryTransportErrorRemainsCoordinatorStageEvidenceAfterCancel(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "worker-client-coordinator-transport-cause"
	assignments, err := BuildCoordinatorAssignments(cfg, 41)
	if err != nil {
		t.Fatalf("BuildCoordinatorAssignments() error = %v", err)
	}
	ordinaryErr := errors.New("injected ordinary status transport error")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workerID := workerID
		transport := workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
			if workerID == 0 {
				cancel()
				return nil, ordinaryErr
			}
			encoded, marshalErr := json.Marshal(WorkerStatus{
				RunID: assignments[workerID].RunID, AssignmentID: assignments[workerID].AssignmentID,
				Generation: assignments[workerID].Generation, WorkerID: uint64(workerID),
				WorkerCount: coordinatorWorkerCount, Phase: WorkerPhaseRunning, TrafficReady: true,
			})
			if marshalErr != nil {
				return nil, marshalErr
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded)),
			}, nil
		})
		client, clientErr := NewWorkerClient(WorkerClientConfig{
			BaseURL: "http://worker.test", ControlToken: "control-secret",
			HTTPClient: &http.Client{Transport: transport},
		})
		if clientErr != nil {
			t.Fatalf("NewWorkerClient(worker=%d) error = %v", workerID, clientErr)
		}
		workers[workerID] = client
	}
	coordinator := &Coordinator{workers: workers, roundTimeout: time.Second}
	if _, disposition := coordinator.statusRound(ctx, assignments); disposition != coordinatorRoundStageFailed {
		t.Fatalf("statusRound() disposition = %v, want stage failure", disposition)
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

type workerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f workerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type cancelingWorkerResponseBody struct {
	ctx     context.Context
	entered chan struct{}
	once    sync.Once
}

func (b *cancelingWorkerResponseBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*cancelingWorkerResponseBody) Close() error { return nil }

type errorWorkerResponseBody struct{ err error }

func (b *errorWorkerResponseBody) Read([]byte) (int, error) { return 0, b.err }
func (*errorWorkerResponseBody) Close() error               { return nil }

type cancelThenErrorWorkerResponseBody struct {
	cancel context.CancelFunc
	err    error
}

func (b *cancelThenErrorWorkerResponseBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, b.err
}

func (*cancelThenErrorWorkerResponseBody) Close() error { return nil }
