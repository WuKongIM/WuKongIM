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
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The optional local callback is deliberately discovered reflectively: the
// unchanged producer must compile and report a missing-seam assertion RED.
func r28SetHarnessReporter(t *testing.T, cfg *WorkerServerConfig, reporter func()) {
	t.Helper()
	field := reflect.ValueOf(cfg).Elem().FieldByName("ReportHarnessFailure")
	if !field.IsValid() || !field.CanSet() || field.Type() != reflect.TypeOf((func())(nil)) {
		t.Fatal("missing narrow ReportHarnessFailure func() snapshot notification")
	}
	field.Set(reflect.ValueOf(reporter))
}

func r28HarnessSnapshot(code FailureCode) WorkerSnapshot {
	return WorkerSnapshot{
		Harness: WorkerHarnessSnapshot{Failures: 1, OfferedUnderdelivery: 1, Classification: SyncClassificationHarnessInvalid},
		Evidence: EvidenceSnapshot{Classification: SyncClassificationHarnessInvalid, Classes: []EvidenceClassSnapshot{{
			Class: FailureClassHarness, Count: 1,
			First: []EvidenceExample{{Stage: EvidenceStageCapacity, Code: code, Value: 1}},
		}}},
	}
}

func r28HTTP(t *testing.T, client *http.Client, base, method, path, bearer string, body any) (int, []byte) {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, base+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("bounded HTTP request failed: %T", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, workerMaxResponseBytes+1))
	if err != nil || int64(len(raw)) > workerMaxResponseBytes {
		t.Fatal("HTTP response bound/read failed")
	}
	return response.StatusCode, raw
}

func r28AssignStart(t *testing.T, client *http.Client, url string, fence WorkerFence) {
	t.Helper()
	cfg := LocalConfig()
	cfg.RunID = fence.RunID
	for _, control := range []struct {
		path string
		body any
	}{
		{"/v1/chat-lifecycle/assign", WorkerAssignment{WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(cfg.Workload.Workers), Config: cfg, CoordinatorGrants: true}},
		{"/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence}},
	} {
		status, _ := r28HTTP(t, client, url, http.MethodPost, control.path, "control-secret", control.body)
		if status != http.StatusOK {
			t.Fatalf("control status=%d", status)
		}
	}
}

func TestWorkerRuntimeTraceSnapshotNotifiesDispatchDeadlineEvidence(t *testing.T) {
	for _, code := range []FailureCode{37, 38} {
		t.Run(failureCodeName(code), func(t *testing.T) {
			generation := newFakeWorkerGeneration()
			generation.snapshot = r28HarnessSnapshot(code)
			var notifications atomic.Uint32
			var held atomic.Bool
			var server *WorkerServer
			cfg := WorkerServerConfig{ControlToken: "control-secret", Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return generation, nil })}
			r28SetHarnessReporter(t, &cfg, func() {
				if !server.mu.TryLock() {
					held.Store(true)
				} else {
					server.mu.Unlock()
				}
				notifications.Add(1)
			})
			var err error
			server, err = NewWorkerServer(cfg)
			if err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			fence := WorkerFence{RunID: "r28-snapshot", AssignmentID: "r28-assignment", Generation: 7}
			r28AssignStart(t, client, httpServer.URL, fence)
			for index := uint64(1); index <= 2; index++ {
				status, raw := r28HTTP(t, client, httpServer.URL, http.MethodGet, "/v1/chat-lifecycle/snapshot", "control-secret", nil)
				var snapshot WorkerSnapshot
				if status != http.StatusOK || json.Unmarshal(raw, &snapshot) != nil {
					t.Fatal("valid R27-shaped snapshot failed")
				}
				if snapshot.SnapshotSequence != index || snapshot.Harness.Failures != 1 || snapshot.Harness.OfferedUnderdelivery != 1 || snapshot.Messages != (WorkerMessageSnapshot{}) {
					t.Fatal("notification changed native snapshot counters or sequence")
				}
				if bytes.Contains(raw, []byte("runtime_trace")) {
					t.Fatal("private runtime trace entered worker wire")
				}
			}
			if notifications.Load() == 0 || held.Load() {
				t.Fatal("valid snapshot missed notification or callback held lifecycle mutex")
			}
			status, raw := r28HTTP(t, client, httpServer.URL, http.MethodGet, "/v1/info", "control-secret", nil)
			var info WorkerInfo
			if status != http.StatusOK || json.Unmarshal(raw, &info) != nil || info.ProtocolVersion != 15 {
				t.Fatal("worker wire version changed")
			}
			status, _ = r28HTTP(t, client, httpServer.URL, http.MethodPost, "/v1/chat-lifecycle/stop", "control-secret", WorkerStopRequest{WorkerFence: fence})
			if status != http.StatusOK {
				t.Fatal("snapshot notification delayed ordinary Stop")
			}
		})
	}
}

func TestWorkerRuntimeTraceSnapshotRejectsUnauthenticatedFailedAndInvalidResults(t *testing.T) {
	for _, kind := range []string{"unauthenticated", "runtime_error", "invalid_shape", "healthy", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			generation := newFakeWorkerGeneration()
			generation.snapshot = r28HarnessSnapshot(38)
			if kind == "runtime_error" {
				generation.snapshotErr = errors.New("redacted runtime failure")
			}
			if kind == "invalid_shape" {
				generation.snapshot.Messages.FirstAttemptDeadlineResolved = 1
			}
			if kind == "healthy" {
				generation.snapshot = WorkerSnapshot{}
			}
			var notifications atomic.Uint32
			cfg := WorkerServerConfig{ControlToken: "control-secret", Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return generation, nil })}
			r28SetHarnessReporter(t, &cfg, func() { notifications.Add(1) })
			server, err := NewWorkerServer(cfg)
			if err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			fence := WorkerFence{RunID: "r28-negative", AssignmentID: "r28-assignment", Generation: 7}
			r28AssignStart(t, client, httpServer.URL, fence)
			token := "control-secret"
			if kind == "unauthenticated" {
				token = ""
			}
			var status int
			if kind == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				request := httptest.NewRequest(http.MethodGet, "/v1/chat-lifecycle/snapshot", nil).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer control-secret")
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				status = response.Code
			} else {
				status, _ = r28HTTP(t, client, httpServer.URL, http.MethodGet, "/v1/chat-lifecycle/snapshot", token, nil)
			}
			if kind == "unauthenticated" && status != http.StatusUnauthorized {
				t.Fatal("authentication bypass")
			}
			if (kind == "runtime_error" || kind == "invalid_shape") && status != http.StatusInternalServerError {
				t.Fatal("invalid snapshot accepted")
			}
			if kind == "healthy" && status != http.StatusOK {
				t.Fatal("healthy snapshot failed")
			}
			if notifications.Load() != 0 {
				t.Fatal("non-successful or healthy snapshot notified trace")
			}
			// Stop still needs a valid final snapshot and remains independent.
			generation.snapshotErr = nil
			generation.snapshot = WorkerSnapshot{}
			r28HTTP(t, client, httpServer.URL, http.MethodPost, "/v1/chat-lifecycle/stop", "control-secret", WorkerStopRequest{WorkerFence: fence})
		})
	}
}

func TestWorkerRuntimeTraceSnapshotOldFenceCannotNotify(t *testing.T) {
	old := newBlockingControlGeneration("snapshot")
	old.snapshot = r28HarnessSnapshot(38)
	fresh := newFakeWorkerGeneration()
	var created atomic.Uint32
	var notifications atomic.Uint32
	cfg := WorkerServerConfig{ControlToken: "control-secret", Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) {
		if created.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	})}
	r28SetHarnessReporter(t, &cfg, func() { notifications.Add(1) })
	server, err := NewWorkerServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	fence := WorkerFence{RunID: "r28-old-fence", AssignmentID: "r28-old", Generation: 7}
	r28AssignStart(t, client, httpServer.URL, fence)
	var release sync.Once
	unblock := func() { release.Do(func() { close(old.controlRelease) }) }
	defer unblock()
	statusResult := make(chan int, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/chat-lifecycle/snapshot", nil)
		request.Header.Set("Authorization", "Bearer control-secret")
		response, requestErr := client.Do(request)
		if requestErr != nil {
			statusResult <- 0
			return
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		statusResult <- response.StatusCode
	}()
	select {
	case <-old.controlStarted:
	case <-time.After(time.Second):
		t.Fatal("old snapshot never entered real generation")
	}
	r28HTTP(t, client, httpServer.URL, http.MethodPost, "/v1/chat-lifecycle/stop", "control-secret", WorkerStopRequest{WorkerFence: fence})
	// The stopped old generation itself had harness evidence, but this port is
	// deliberately on successful Snapshot GET, not every native Stop response.
	fence.AssignmentID, fence.Generation = "r28-new", 8
	r28AssignStart(t, client, httpServer.URL, fence)
	unblock()
	select {
	case status := <-statusResult:
		if status != http.StatusConflict {
			t.Fatalf("stale result status=%d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("stale snapshot did not join")
	}
	if notifications.Load() != 0 {
		t.Fatal("old-generation snapshot exported as current")
	}
	r28HTTP(t, client, httpServer.URL, http.MethodPost, "/v1/chat-lifecycle/stop", "control-secret", WorkerStopRequest{WorkerFence: fence})
}

func TestWorkerRuntimeTraceFinalSnapshotFailureCanNotify(t *testing.T) {
	generation := newFakeWorkerGeneration()
	generation.snapshot = r28HarnessSnapshot(38)
	var notifications atomic.Uint32
	cfg := WorkerServerConfig{ControlToken: "control-secret", Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return generation, nil })}
	r28SetHarnessReporter(t, &cfg, func() { notifications.Add(1) })
	server, err := NewWorkerServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	fence := WorkerFence{RunID: "r28-final-snapshot", AssignmentID: "r28-final", Generation: 7}
	r28AssignStart(t, client, httpServer.URL, fence)
	status, _ := r28HTTP(t, client, httpServer.URL, http.MethodPost, "/v1/chat-lifecycle/stop", "control-secret", WorkerStopRequest{WorkerFence: fence})
	if status != http.StatusOK {
		t.Fatal("ordinary Stop failed")
	}
	status, _ = r28HTTP(t, client, httpServer.URL, http.MethodGet, "/v1/chat-lifecycle/snapshot", "control-secret", nil)
	if status != http.StatusOK || notifications.Load() == 0 {
		t.Fatal("successful cached final failure snapshot missed notification")
	}
}
