package capacity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestStopWorkerPostsExactAssignmentAndRequiresTerminalResponse(t *testing.T) {
	var got worker.StopRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		writeCapacityJSON(t, w, worker.Status{
			Phase: worker.PhaseStopped,
			Assignment: worker.Assignment{
				RunID:        got.RunID,
				AssignmentID: got.AssignmentID,
				WorkerID:     "worker-a",
			},
		})
	}))
	t.Cleanup(server.Close)

	err := stopWorker(context.Background(), model.Worker{ID: "worker-a", Addr: server.URL, InsecureControl: true}, worker.StopRequest{
		RunID:        "run-a",
		AssignmentID: "generation-a",
	})

	require.NoError(t, err)
	require.Equal(t, worker.StopRequest{RunID: "run-a", AssignmentID: "generation-a"}, got)
}

func TestStopWorkerRejectsMissingIdentityAndNonSuccessStatus(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "assignment mismatch", http.StatusConflict)
	}))
	t.Cleanup(server.Close)
	w := model.Worker{ID: "worker-a", Addr: server.URL, InsecureControl: true}

	err := stopWorker(context.Background(), w, worker.StopRequest{RunID: "run-a"})
	require.ErrorContains(t, err, "exact run_id and assignment_id")
	require.False(t, called, "an incomplete identity must not reach the worker")

	err = stopWorker(context.Background(), w, worker.StopRequest{RunID: "run-a", AssignmentID: "generation-a"})
	require.ErrorContains(t, err, "409 Conflict")
	require.ErrorContains(t, err, "assignment mismatch")
}

func TestStopWorkerRejectsMatchingButNonTerminalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCapacityJSON(t, w, worker.Status{
			Phase:       worker.PhaseRun,
			ActivePhase: worker.PhaseRun,
			Assignment: worker.Assignment{
				RunID:        "run-a",
				AssignmentID: "generation-a",
				WorkerID:     "worker-a",
			},
		})
	}))
	t.Cleanup(server.Close)

	err := stopWorker(
		context.Background(),
		model.Worker{ID: "worker-a", Addr: server.URL, InsecureControl: true},
		worker.StopRequest{RunID: "run-a", AssignmentID: "generation-a"},
	)

	require.ErrorContains(t, err, "not terminal")
}

func TestRunnerStopWorkersFromStatusUsesExactCurrentGeneration(t *testing.T) {
	var got worker.StopRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/status":
			writeCapacityJSON(t, w, worker.Status{
				Phase:       worker.PhaseRun,
				ActivePhase: worker.PhaseRun,
				Assignment: worker.Assignment{
					RunID:        "run-a",
					AssignmentID: "generation-a",
					WorkerID:     "worker-a",
				},
			})
		case "/v1/stop":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			writeCapacityJSON(t, w, worker.Status{
				Phase: worker.PhaseStopped,
				Assignment: worker.Assignment{
					RunID:        got.RunID,
					AssignmentID: got.AssignmentID,
					WorkerID:     "worker-a",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	runner := &Runner{workers: []model.Worker{{ID: "worker-a", Addr: server.URL, InsecureControl: true}}}

	err := runner.stopWorkersFromStatus()

	require.NoError(t, err)
	require.Equal(t, worker.StopRequest{RunID: "run-a", AssignmentID: "generation-a"}, got)
}

func TestStopCapacityWorkersAllowsIdleWorkerAfterAssignmentFailure(t *testing.T) {
	stopCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/status":
			writeCapacityJSON(t, w, worker.Status{Phase: worker.PhaseIdle})
		case "/v1/stop":
			stopCalls++
			http.Error(w, "idle worker must not receive stop", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := stopCapacityWorkers(
		[]model.Worker{{ID: "worker-a", Addr: server.URL, InsecureControl: true}},
		worker.StopRequest{RunID: "run-a", AssignmentID: "generation-a"},
	)

	require.NoError(t, err)
	require.Zero(t, stopCalls)
}

func TestStopCapacityWorkersRejectsDifferentGenerationWithoutStoppingIt(t *testing.T) {
	stopCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/status":
			writeCapacityJSON(t, w, worker.Status{
				Phase: worker.PhaseStopped,
				Assignment: worker.Assignment{
					RunID:        "run-a",
					AssignmentID: "new-generation",
					WorkerID:     "worker-a",
				},
			})
		case "/v1/stop":
			stopCalls++
			http.Error(w, "must not stop a different generation", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := stopCapacityWorkers(
		[]model.Worker{{ID: "worker-a", Addr: server.URL, InsecureControl: true}},
		worker.StopRequest{RunID: "run-a", AssignmentID: "old-generation"},
	)

	require.ErrorContains(t, err, "cleanup identity conflict")
	require.Zero(t, stopCalls)
}

func TestEvaluateAttemptClassifiesPassAndFailureReasons(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Duration = time.Second
	attempt := Attempt{Index: 0, OfferedQPS: 100}
	rep := report.Report{
		Summary: report.Summary{},
		Metrics: metrics.SnapshotData{
			Counters: map[string]uint64{
				"person_send_success_total{channel_type=person,phase=run,profile=p,traffic=t}": 100,
			},
			Histograms: map[string]metrics.HistogramSummary{
				"person_send_latency_seconds{channel_type=person,phase=run,profile=p,traffic=t}": {P50Seconds: 0.010, P95Seconds: 0.020, P99Seconds: 0.030},
			},
		},
	}

	got := EvaluateAttempt(cfg, attempt, rep)

	require.True(t, got.Passed)
	require.Equal(t, 100.0, got.ActualQPS)
	require.Equal(t, uint64(100), got.ScheduledMessages)
	require.Equal(t, uint64(100), got.SendSuccess)
	require.Equal(t, uint64(0), got.BacklogMessages)
	require.Equal(t, 30*time.Millisecond, got.SendackP99)

	attempt.OfferedQPS = 200
	got = EvaluateAttempt(cfg, attempt, rep)
	require.Equal(t, uint64(200), got.ScheduledMessages)
	require.Equal(t, uint64(100), got.SendSuccess)
	require.Equal(t, uint64(100), got.BacklogMessages)

	attempt.OfferedQPS = 100
	cfg.StableP99 = time.Millisecond
	got = EvaluateAttempt(cfg, attempt, rep)
	require.False(t, got.Passed)
	require.Equal(t, "sendack_p99_exceeded", got.FailureReason)
}

func TestRunAttemptReturnsCoordinatorErrorWhenNoAttemptReport(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeCapacityJSON(t, w, model.BenchCapabilities{Enabled: true, Version: "bench/v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{api.URL}
	cfg.GatewayTCPAddrs = []string{"127.0.0.1:1"}
	cfg.Profile = ProfilePerson
	cfg.StartQPS = 1
	cfg.MaxQPS = 1
	cfg.Duration = 100 * time.Millisecond
	cfg.Warmup = 100 * time.Millisecond
	cfg.Cooldown = 0
	cfg.StableP99 = time.Second
	cfg.ReportDir = t.TempDir()
	runner := NewRunner(cfg, DiscoveredTarget{
		Target: model.Target{
			Name:     "capacity-send",
			API:      model.TargetAPIConfig{Addrs: []string{api.URL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:1"}}},
			BenchAPI: model.BenchAPIConfig{Enabled: true, Addrs: []string{api.URL}},
		},
	})

	require.NoError(t, runner.startWorker())
	defer runner.close()
	got, err := runner.RunAttempt(context.Background(), Attempt{Index: 0, OfferedQPS: 1})

	require.Error(t, err)
	require.False(t, got.Passed)
	require.Equal(t, "preflight_failed", got.FailureReason)
	require.Contains(t, err.Error(), "target bench api capabilities missing")
}

func TestRunAttemptStopsWorkerSoNextAttemptCanAssign(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeCapacityJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{model.ChannelTypeGroup},
				},
			})
		case "/bench/v1/snapshot":
			writeCapacityJSON(t, w, model.BenchSnapshot{Version: "bench/v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	workerServer := httptest.NewServer(worker.NewServer(worker.Config{
		InsecureControl: true,
		WorkloadRunner:  noopCapacityWorkloadRunner{},
	}))
	defer workerServer.Close()
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{api.URL}
	cfg.Profile = ProfilePerson
	cfg.Duration = time.Millisecond
	cfg.Warmup = time.Millisecond
	cfg.Cooldown = 0
	cfg.ReportDir = t.TempDir()
	runner := NewRunner(cfg, DiscoveredTarget{
		Target: model.Target{
			Name:     "capacity-send",
			API:      model.TargetAPIConfig{Addrs: []string{api.URL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:1"}}},
			BenchAPI: model.BenchAPIConfig{Enabled: true, Addrs: []string{api.URL}},
		},
	})
	runner.workers = []model.Worker{{ID: "local-capacity-worker", Addr: workerServer.URL, Weight: 1, InsecureControl: true}}

	_, err := runner.RunAttempt(context.Background(), Attempt{Index: 0, OfferedQPS: 1})
	require.NoError(t, err)
	_, err = runner.RunAttempt(context.Background(), Attempt{Index: 1, OfferedQPS: 1})
	require.NoError(t, err)
}

type noopCapacityWorkloadRunner struct{}

func (noopCapacityWorkloadRunner) Prepare(context.Context, worker.Assignment) error  { return nil }
func (noopCapacityWorkloadRunner) Connect(context.Context, worker.Assignment) error  { return nil }
func (noopCapacityWorkloadRunner) Warmup(context.Context, worker.Assignment) error   { return nil }
func (noopCapacityWorkloadRunner) Run(context.Context, worker.Assignment) error      { return nil }
func (noopCapacityWorkloadRunner) Cooldown(context.Context, worker.Assignment) error { return nil }
