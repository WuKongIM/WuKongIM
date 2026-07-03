package capacity

import (
	"context"
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
