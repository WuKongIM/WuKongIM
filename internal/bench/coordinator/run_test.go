package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorAssignsWorkersAndRunsPhases(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[0].ObservedPhases())
	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[1].ObservedPhases())
}

func TestCoordinatorAssignmentIncludesWorkerShard(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	assignment := workers[0].Assignment()
	require.Equal(t, "run-1", assignment.RunID)
	require.Equal(t, "a", assignment.WorkerID)
	require.Equal(t, "a", assignment.Plan.WorkerID)
	require.Contains(t, assignment.Plan.Profiles, "group-hot")
	require.Equal(t, result.Plan.ChannelOwners, assignment.ChannelOwners)
	require.Contains(t, assignment.ChannelOwners, "group-hot")
	require.Equal(t, fakeTargetOK().Gateway.TCP.Addrs, assignment.Target.Gateway.TCP.Addrs)
	require.Equal(t, "wkbench/v1", assignment.Scenario.Version)
}

func TestCoordinatorAssignmentCopiesOnlyCurrentWorkerClientProfile(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	configs := workers.ClientConfigs()
	configs[0].Client = &model.WorkerClientConfig{SendQueueCapacity: 16, MaxInflight: 1, ReadBufferSize: 1024, FrameBufferSize: 4}
	configs[1].Client = &model.WorkerClientConfig{SendQueueCapacity: 32, MaxInflight: 2, ReadBufferSize: 2048, FrameBufferSize: 8}
	coord := New(CoordinatorConfig{
		Workers:      configs,
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, configs[0].Client, workers[0].Assignment().Client)
	require.Equal(t, configs[1].Client, workers[1].Assignment().Client)
	encoded, err := json.Marshal(workers[0].Assignment())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "control_token")
	require.NotContains(t, string(encoded), `"secret"`)
}

func TestCloneWorkerClientConfigReturnsIndependentCopy(t *testing.T) {
	original := &model.WorkerClientConfig{SendQueueCapacity: 16, MaxInflight: 1, ReadBufferSize: 1024, FrameBufferSize: 4}

	cloned := cloneWorkerClientConfig(original)
	original.SendQueueCapacity = 999

	require.NotSame(t, original, cloned)
	require.Equal(t, 16, cloned.SendQueueCapacity)
}

func TestCoordinatorAssignmentCopiesOnlyCurrentWorkerTCPSourcePool(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	configs := workers.ClientConfigs()
	configs[0].TCPSource = &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1", "192.168.3.57"}, PortMin: 1024, PortMax: 65535}
	configs[1].TCPSource = &model.TCPSourceConfig{IPv4Addrs: []string{"10.0.0.10"}, PortMin: 20000, PortMax: 65535}
	coord := New(CoordinatorConfig{
		Workers:      configs,
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, configs[0].TCPSource, workers[0].Assignment().TCPSource)
	require.Equal(t, configs[1].TCPSource, workers[1].Assignment().TCPSource)
	require.NotSame(t, configs[0].TCPSource, workers[0].Assignment().TCPSource)
	require.NotSame(t, &configs[0].TCPSource.IPv4Addrs[0], &workers[0].Assignment().TCPSource.IPv4Addrs[0])
	encoded, err := json.Marshal(workers[0].Assignment())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "control_token")
	require.NotContains(t, string(encoded), `"secret"`)
}

func TestCloneWorkerTCPSourceConfigReturnsIndependentCopy(t *testing.T) {
	original := &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1", "192.168.3.57"}, PortMin: 1024, PortMax: 65535}

	cloned := cloneWorkerTCPSourceConfig(original)
	original.IPv4Addrs[0] = "10.0.0.1"
	original.PortMin = 9999

	require.NotSame(t, original, cloned)
	require.Equal(t, []string{"127.0.0.1", "192.168.3.57"}, cloned.IPv4Addrs)
	require.Equal(t, 1024, cloned.PortMin)
}

func TestCoordinatorStopsAssignedWorkersWhenLaterAssignmentFails(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].SetMetrics(metrics.SnapshotData{Counters: map[string]uint64{"person_send_success_total{phase=run}": 999}})
	workers[0].SetReport(json.RawMessage(`{"worker_id":"a","report":{"run_id":"old-run"}}`))
	workers[1].FailAssign(http.StatusInternalServerError, "assign exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()
	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Contains(t, err.Error(), "assign exploded")
	require.True(t, workers[0].Stopped(), "already assigned worker should be stopped")
	require.False(t, workers[1].Stopped(), "worker that rejected assignment was not active")
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "b", result.Report.WorkerFailures[0].WorkerID)
	require.Equal(t, "assign", result.Report.WorkerFailures[0].Phase)
	require.Equal(t, "worker_assignment_failed", result.Report.WorkerFailures[0].ReasonCode)
	require.Zero(t, result.Report.Summary.SendSuccess, "assignment failure must not reuse metrics from another run")
	require.Empty(t, result.Report.WorkerMetrics)
	require.Empty(t, result.Report.WorkerReports)
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "diagnostic-summary.json"))
}

func TestCoordinatorReturnsWorkerFailedWhenPhasePostFails(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[1].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Contains(t, err.Error(), "warmup exploded")
	require.True(t, workers[0].Stopped(), "healthy workers should be stopped on fail-fast failure")
	require.True(t, workers[1].Stopped(), "failed worker should be stopped on fail-fast failure")
}

func TestCoordinatorFailFastFalseAttemptsRemainingWorkersAndPhases(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].FailPhase(PhasePrepare, http.StatusInternalServerError, "prepare exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  20 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = false

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.False(t, workers[0].Stopped(), "fail_fast=false should not stop all workers on first phase error")
	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[1].ObservedPhases())
	require.False(t, workers[0].SawPhaseAttempt(PhaseCooldown), "failed workers should be skipped after their first failure")
}

func TestCoordinatorFailFastPhaseFailureStillWritesReport(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[1].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Equal(t, 1, result.Report.Summary.WorkerFailed)
	require.Equal(t, report.ExitWorkerFailed, result.Report.ExitCode)
	require.Len(t, result.Report.WorkerMetrics, 2)
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "report.json"))
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "workers", "a.report.json"))
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "workers", "b.report.json"))
}

func TestCoordinatorFailFastReportWriteFailureOverridesWorkerFailure(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	reportDir := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(reportDir, []byte("file blocks report dir"), 0o600))
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true
	scenario.Run.ReportDir = reportDir

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusInternalFailed, result.Status)
	require.Contains(t, err.Error(), "warmup exploded")
}

func TestCoordinatorCollectsFinalTargetSnapshot(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bench/v1/snapshot" {
			writeRunTestJSON(w, model.BenchSnapshot{Version: "bench/v1", Counts: map[string]int{"users": 7}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(targetServer.Close)
	tgt := fakeTargetOK()
	tgt.BenchAPI.Addrs = []string{targetServer.URL}
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       tgt,
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Len(t, result.Report.TargetSnapshots, 1)
	require.JSONEq(t, `{"version":"bench/v1","counts":{"users":7}}`, string(result.Report.TargetSnapshots[0]))
	data, err := os.ReadFile(filepath.Join(scenario.Run.ReportDir, "metrics", "target-snapshots.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"users":7`)
}

func TestCoordinatorCollectsPresenceSnapshotsWhenSupported(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/snapshot":
			writeRunTestJSON(w, model.BenchSnapshot{Version: "bench/v1"})
		case "/bench/v1/capabilities":
			writeRunTestJSON(w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					Snapshot:         true,
					PresenceSnapshot: true,
				},
			})
		case "/bench/v1/presence/snapshot":
			writeRunTestJSON(w, model.PresenceSnapshot{
				Version:                   "bench/v1",
				NodeID:                    1,
				OwnerRoutesActive:         4,
				AuthorityRoutesActive:     6,
				AuthorityRoutesByHashSlot: map[uint16]int{9: 6},
				TouchRoutesTotal:          11,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(targetServer.Close)
	tgt := fakeTargetOK()
	tgt.BenchAPI.Addrs = []string{targetServer.URL}
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       tgt,
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, []model.PresenceSnapshot{{
		Version:                   "bench/v1",
		NodeID:                    1,
		OwnerRoutesActive:         4,
		AuthorityRoutesActive:     6,
		AuthorityRoutesByHashSlot: map[uint16]int{9: 6},
		TouchRoutesTotal:          11,
	}}, result.Report.PresenceSnapshots)
	data, err := os.ReadFile(filepath.Join(scenario.Run.ReportDir, "report.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"presence_snapshots"`)
	require.Contains(t, string(data), `"owner_routes_active": 4`)
}

func TestCoordinatorCollectsPresenceSnapshotsFromMixedTargets(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	oldTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/snapshot":
			writeRunTestJSON(w, model.BenchSnapshot{Version: "bench/v1"})
		case "/bench/v1/capabilities":
			writeRunTestJSON(w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					Snapshot: true,
				},
			})
		case "/bench/v1/presence/snapshot":
			http.Error(w, "presence snapshot is not configured", http.StatusNotImplemented)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(oldTarget.Close)
	newTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/snapshot":
			writeRunTestJSON(w, model.BenchSnapshot{Version: "bench/v1"})
		case "/bench/v1/capabilities":
			writeRunTestJSON(w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					Snapshot:         true,
					PresenceSnapshot: true,
				},
			})
		case "/bench/v1/presence/snapshot":
			writeRunTestJSON(w, model.PresenceSnapshot{Version: "bench/v1", NodeID: 2, OwnerRoutesActive: 8})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(newTarget.Close)
	tgt := fakeTargetOK()
	tgt.BenchAPI.Addrs = []string{oldTarget.URL, newTarget.URL}
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       tgt,
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, []model.PresenceSnapshot{{Version: "bench/v1", NodeID: 2, OwnerRoutesActive: 8}}, result.Report.PresenceSnapshots)
	require.Empty(t, result.Report.ErrorSamples)
}

func TestCoordinatorRecordsTargetSnapshotErrorSample(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	tgt := fakeTargetOK()
	tgt.BenchAPI.Addrs = []string{"http://127.0.0.1:1"}
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       tgt,
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Empty(t, result.Report.TargetSnapshots)
	require.NotEmpty(t, result.Report.ErrorSamples)
	require.Equal(t, "target_metrics_error", result.Report.ErrorSamples[0].Name)
}

func TestCoordinatorWorkerFailedSummaryCountsDistinctWorkers(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].FailPhase(PhasePrepare, http.StatusInternalServerError, "prepare exploded")
	workers[0].FailPhase(PhaseConnect, http.StatusInternalServerError, "connect exploded")
	workers[0].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  20 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = false

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Equal(t, 1, result.Report.Summary.WorkerFailed)
}

func TestCoordinatorPhaseAndCollectionFailuresCountDistinctWorkerUnion(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	workers[1].FailReport(http.StatusInternalServerError, "report exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true
	scenario.Limits.Hard.MaxWorkerFailed = 1

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusHardLimitFailed, result.Status)
	require.Equal(t, 2, result.Report.Summary.WorkerFailed)
	require.Equal(t, report.ExitHardLimitFailed, result.Report.ExitCode)
}

func TestCoordinatorReportCollectionCancellationReturnsCanceled(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	workers[0].CancelWhenMetricsPolled(cancel)

	result, err := coord.Run(ctx, fakeScenario())

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, StatusCanceled, result.Status)
}

func TestCoordinatorWorkerMetricsCollectionFailureFailsSuccessfulRun(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailMetrics(http.StatusInternalServerError, "metrics exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Equal(t, 1, result.Report.Summary.WorkerFailed)
	require.Equal(t, report.ExitWorkerFailed, result.Report.ExitCode)
	require.NotEmpty(t, result.Report.WorkerMetrics)
	require.NotEmpty(t, result.Report.WorkerMetrics[0].Metrics.Errors)
	require.Equal(t, "worker_metrics_error", result.Report.WorkerMetrics[0].Metrics.Errors[0].Name)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "a", result.Report.WorkerFailures[0].WorkerID)
	require.Equal(t, "collect", result.Report.WorkerFailures[0].Phase)
	require.Equal(t, "worker_metrics_unavailable", result.Report.WorkerFailures[0].ReasonCode)
}

func TestCoordinatorWorkerReportCollectionFailureFailsSuccessfulRunWithFallback(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailReport(http.StatusInternalServerError, "report exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Equal(t, 1, result.Report.Summary.WorkerFailed)
	require.Equal(t, report.ExitWorkerFailed, result.Report.ExitCode)
	require.Len(t, result.Report.WorkerReports, 1)
	require.JSONEq(t, `{"worker_id":"a","error":"worker report unavailable"}`, string(result.Report.WorkerReports[0].Report))
	require.NotEmpty(t, result.Report.ErrorSamples)
	require.Equal(t, "worker_report_error", result.Report.ErrorSamples[0].Name)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "a", result.Report.WorkerFailures[0].WorkerID)
	require.Equal(t, "collect", result.Report.WorkerFailures[0].Phase)
	require.Equal(t, "worker_report_unavailable", result.Report.WorkerFailures[0].ReasonCode)
}

func TestCoordinatorCollectWorkerReportsUnwrapsWorkerEnvelopeAndNormalizesID(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].SetReport(json.RawMessage(`{"worker_id":"worker-endpoint-id","report":{"run_id":"run-1","phase":"run","metrics":{"counters":{"send_success_total":1}}}}`))
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	_, workerReports, collectionFailures, failureDetails, err := coord.collectWorkerReports(context.Background())

	require.NoError(t, err)
	require.Empty(t, collectionFailures)
	require.Empty(t, failureDetails)
	require.Len(t, workerReports, 1)
	require.Equal(t, "a", workerReports[0].WorkerID)
	require.JSONEq(t, `{"run_id":"run-1","phase":"run","metrics":{"counters":{"send_success_total":1}}}`, string(workerReports[0].Report))
}

func TestCoordinatorHardLimitTakesPriorityOverWorkerReportCollectionFailure(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].SetMetrics(metrics.SnapshotData{Counters: map[string]uint64{
		"person_send_success_total": 9,
		"person_send_error_total":   1,
	}})
	workers[0].FailReport(http.StatusInternalServerError, "report exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Limits.Hard.MaxSendackErrorRate = 0

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusHardLimitFailed, result.Status)
	require.Equal(t, report.ExitHardLimitFailed, result.Report.ExitCode)
	require.Equal(t, 1, result.Report.Summary.WorkerFailed)
	require.NotEmpty(t, result.Report.Violations)
}

func TestCoordinatorStopsWorkersOnContextCancellation(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].BlockStatus(PhaseConnect)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	workers[0].CancelWhenStatusPolled(cancel)

	result, err := coord.Run(ctx, fakeScenario())

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, StatusCanceled, result.Status)
	require.True(t, workers[0].Stopped())
	require.True(t, workers[1].Stopped())
}

func TestCoordinatorParentDeadlineReturnsCanceled(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].BlockStatus(PhaseConnect)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := coord.Run(ctx, fakeScenario())

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, StatusCanceled, result.Status)
	require.True(t, workers[0].Stopped())
	require.True(t, workers[1].Stopped())
}

func TestCoordinatorPollTimeoutReturnsWorkerFailed(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].LagPhase(PhasePrepare)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
}

func TestCoordinatorWaitsForCompletedPhaseBeforeAdvancing(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].KeepPhaseInProgress(PhasePrepare)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.False(t, workers[0].SawPhaseAttempt(PhaseConnect), "coordinator must not advance before prepare completes")
}

func TestCoordinatorPollTimeoutIncludesScenarioPhaseDurations(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].CompletePhaseAfter(PhaseRun, 20*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.Duration = 50 * time.Millisecond

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].SawPhaseAttempt(PhaseCooldown))
}

func TestCoordinatorPollTimeoutIncludesChurnReconnectSchedule(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].CompletePhaseAfter(PhaseRun, 70*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  10 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.Duration = 40 * time.Millisecond
	scenario.Identity.TotalUsers = 20
	scenario.Online.ConnectRate = model.Rate{PerSecond: 500}
	scenario.Online.Churn = model.ChurnConfig{
		Enabled:           true,
		Interval:          10 * time.Millisecond,
		Ratio:             0.5,
		SameUserRatio:     0.5,
		IdentitySwapRatio: 0.5,
	}

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].SawPhaseAttempt(PhaseCooldown))
}

func TestChurnReconnectScheduleDurationUsesBusiestWorker(t *testing.T) {
	scenario := fakeScenario()
	scenario.Run.Duration = 30 * time.Minute
	scenario.Online.ConnectRate = model.Rate{PerSecond: 2}
	scenario.Online.Churn = model.ChurnConfig{
		Enabled:  true,
		Interval: 5 * time.Minute,
		Ratio:    0.01,
	}
	plan := model.Plan{Workers: map[string]model.WorkerPlan{
		"smaller": {IdentityRange: model.Range{Start: 0, End: 400}},
		"busiest": {IdentityRange: model.Range{Start: 400, End: 1000}},
	}}

	require.Equal(t, 15*time.Second, churnReconnectScheduleDuration(scenario, plan))
}

func TestCoordinatorPollTimeoutIncludesConnectSchedule(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].CompletePhaseAfter(PhaseConnect, 12*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Online.TotalUsers = 10
	scenario.Online.ConnectRate = model.Rate{PerSecond: 1000}

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].SawPhaseAttempt(PhaseWarmup))
}

func TestCoordinatorFailsWhenWorkerStatusReportsPhaseError(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhaseAfterAccept(PhaseConnect, "connect exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Contains(t, err.Error(), "connect exploded")
	require.False(t, workers[0].SawPhaseAttempt(PhaseWarmup), "coordinator must not advance after status reports a phase error")
}

func TestCoordinatorWritesStructuredPhaseFailureForDiagnosis(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhaseAfterAccept(PhaseConnect, "connect exploded")
	reportDir := t.TempDir()
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = reportDir

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	failure := result.Report.WorkerFailures[0]
	require.Equal(t, "a", failure.WorkerID)
	require.Equal(t, string(PhaseConnect), failure.Phase)
	require.Equal(t, "phase_wait_failed", failure.ReasonCode)
	require.Contains(t, failure.Detail, "connect exploded")
	require.False(t, failure.ObservedAt.IsZero())
	require.Len(t, result.Report.PhaseWindows, 2)
	require.Equal(t, string(PhaseConnect), result.Report.PhaseWindows[1].Phase)
	require.False(t, result.Report.PhaseWindows[1].EndedAt.Before(result.Report.PhaseWindows[1].StartedAt))

	data, readErr := os.ReadFile(filepath.Join(reportDir, "diagnostic-summary.json"))
	require.NoError(t, readErr)
	var summary report.DiagnosticSummary
	require.NoError(t, json.Unmarshal(data, &summary))
	require.Len(t, summary.FailedWorkers, 1)
	require.Equal(t, "phase_wait_failed", summary.FailedWorkers[0].ReasonCode)
}

func TestCoordinatorStopAllContinuesWhenOneWorkerStopHangs(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].HangStop()
	workers[1].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  50 * time.Millisecond,
	})

	started := time.Now()
	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.True(t, workers[1].Stopped(), "healthy worker should still receive stop")
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestCoordinatorRejectsMismatchedWorkerStatusAssignment(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].SetStatusAssignment(worker.Assignment{RunID: "wrong-run", WorkerID: "a"})
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Contains(t, err.Error(), "status assignment mismatch")
}

func TestCoordinatorRunsPreflightBeforeAssignment(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	coord := New(CoordinatorConfig{
		Workers: workers.ClientConfigs(),
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return errors.New("preflight denied")
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.ErrorContains(t, err, "preflight denied")
	require.Equal(t, StatusPreflightFailed, result.Status)
	require.False(t, workers[0].Assigned())
}

func TestCoordinatorCollectsWorkerMetricsReportsAndFailsHardLimit(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].SetMetrics(metrics.SnapshotData{Counters: map[string]uint64{
		"person_send_success_total": 9,
		"person_send_error_total":   1,
	}})
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Limits.Hard.MaxSendackErrorRate = 0
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusHardLimitFailed, result.Status)
	require.Equal(t, 0.1, result.Report.Summary.SendackErrorRate)
	require.Len(t, result.Report.WorkerMetrics, 1)
	require.Len(t, result.Report.WorkerReports, 1)
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "workers", "a.report.json"))
}

func TestCoordinatorReportWriteFailureReturnsInternalFailed(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	reportDir := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(reportDir, []byte("file blocks report dir"), 0o600))
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = reportDir

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusInternalFailed, result.Status)
}

func TestCoordinatorReturnsTargetUnavailableForMarkedWorkerError(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhase(PhaseRun, http.StatusServiceUnavailable, `{"error":"target unavailable","reason_code":"target_unavailable"}`)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusTargetUnavailable, result.Status)
}

func TestCoordinatorDoesNotInferTargetUnavailableFromHTTPStatus(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhase(PhaseRun, http.StatusServiceUnavailable, `{"error":"worker overloaded"}`)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_start_failed", result.Report.WorkerFailures[0].ReasonCode)
}

func TestCoordinatorClassifiesWorkerTCPSourceFailuresAsWorkerFailed(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		reasonCode string
	}{
		{
			name:       "unavailable",
			reasonCode: "tcp_source_unavailable",
			err: &benchworkload.TCPSourceError{
				Kind: benchworkload.TCPSourceErrorUnavailable,
				Err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EADDRNOTAVAIL},
			},
		},
		{
			name:       "exhausted",
			reasonCode: "tcp_source_pool_exhausted",
			err:        &benchworkload.TCPSourceError{Kind: benchworkload.TCPSourceErrorExhausted, Capacity: 2, Examined: 2, Conflicts: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workerServer := httptest.NewServer(worker.NewServer(worker.Config{
				ControlToken:   "secret",
				WorkloadRunner: &tcpSourceFailureRunner{connectErr: tt.err},
			}))
			defer workerServer.Close()
			coord := New(CoordinatorConfig{
				Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
				Target:  fakeTargetOK(),
				Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
					return nil
				}),
				PollInterval: time.Millisecond,
				PollTimeout:  100 * time.Millisecond,
			})

			result, err := coord.Run(context.Background(), fakeScenario())

			require.Error(t, err)
			require.Equal(t, StatusWorkerFailed, result.Status)
			require.NotEqual(t, StatusTargetUnavailable, result.Status)
			require.Contains(t, err.Error(), "tcp source")
			require.Len(t, result.Report.WorkerFailures, 1)
			require.Equal(t, tt.reasonCode, result.Report.WorkerFailures[0].ReasonCode)
		})
	}
}

func TestCoordinatorDoesNotInferReasonCodeFromWorkerErrorText(t *testing.T) {
	workerServer := httptest.NewServer(worker.NewServer(worker.Config{
		ControlToken:   "secret",
		WorkloadRunner: &tcpSourceFailureRunner{connectErr: errors.New("tcp source unavailable mentioned by an untyped hook")},
	}))
	defer workerServer.Close()
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_hook_failed", result.Report.WorkerFailures[0].ReasonCode)
}

func TestCoordinatorTargetUnavailableReportUsesTargetExitCode(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailPhase(PhaseRun, http.StatusServiceUnavailable, `{"error":"target unavailable","reason_code":"target_unavailable"}`)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusTargetUnavailable, result.Status)
	require.Equal(t, report.StatusFailed, result.Report.Status)
	require.Equal(t, report.ExitTargetUnavailable, result.Report.ExitCode)
	data, readErr := os.ReadFile(filepath.Join(scenario.Run.ReportDir, "report.json"))
	require.NoError(t, readErr)
	var written report.Report
	require.NoError(t, json.Unmarshal(data, &written))
	require.Equal(t, report.StatusFailed, written.Status)
	require.Equal(t, report.ExitTargetUnavailable, written.ExitCode)
}

type preflightFunc func(context.Context, model.Target, model.WorkerSet) error

type tcpSourceFailureRunner struct {
	connectErr error
}

func (r *tcpSourceFailureRunner) Prepare(context.Context, worker.Assignment) error { return nil }
func (r *tcpSourceFailureRunner) Connect(context.Context, worker.Assignment) error {
	return r.connectErr
}
func (r *tcpSourceFailureRunner) Warmup(context.Context, worker.Assignment) error   { return nil }
func (r *tcpSourceFailureRunner) Run(context.Context, worker.Assignment) error      { return nil }
func (r *tcpSourceFailureRunner) Cooldown(context.Context, worker.Assignment) error { return nil }

func (f preflightFunc) Check(ctx context.Context, target model.Target, workers model.WorkerSet) error {
	return f(ctx, target, workers)
}

type fakeWorkers []*fakeWorker

func newFakeWorkers(t *testing.T, count int) fakeWorkers {
	t.Helper()
	out := make(fakeWorkers, 0, count)
	for i := 0; i < count; i++ {
		fw := &fakeWorker{
			id:              string(rune('a' + i)),
			phase:           worker.PhaseIdle,
			failPhases:      make(map[Phase]fakePhaseFailure),
			blockStatus:     make(map[Phase]bool),
			activePhase:     make(map[Phase]bool),
			completeAfter:   make(map[Phase]time.Duration),
			phaseAcceptedAt: make(map[Phase]time.Time),
			failAfterAccept: make(map[Phase]string),
		}
		fw.server = httptest.NewServer(http.HandlerFunc(fw.handle))
		t.Cleanup(fw.server.Close)
		out = append(out, fw)
	}
	return out
}

func (fws fakeWorkers) ClientConfigs() []model.Worker {
	out := make([]model.Worker, 0, len(fws))
	for _, fw := range fws {
		out = append(out, model.Worker{ID: fw.id, Addr: fw.server.URL, Weight: 1, ControlToken: "secret"})
	}
	return out
}

type fakeWorker struct {
	mu                  sync.Mutex
	id                  string
	server              *httptest.Server
	phase               worker.Phase
	assignment          worker.Assignment
	observed            []Phase
	stopped             bool
	assigned            bool
	hangStop            bool
	statusAssignment    *worker.Assignment
	phaseAttempts       []Phase
	failAssign          *fakePhaseFailure
	failPhases          map[Phase]fakePhaseFailure
	metrics             metrics.SnapshotData
	report              json.RawMessage
	failMetrics         *fakePhaseFailure
	failReport          *fakePhaseFailure
	lagPhases           map[Phase]bool
	blockStatus         map[Phase]bool
	activePhase         map[Phase]bool
	completeAfter       map[Phase]time.Duration
	phaseAcceptedAt     map[Phase]time.Time
	failAfterAccept     map[Phase]string
	cancelOnStatusPoll  context.CancelFunc
	cancelOnMetricsPoll context.CancelFunc
}

func (fw *fakeWorker) SetMetrics(snapshot metrics.SnapshotData) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.metrics = snapshot
}

func (fw *fakeWorker) FailMetrics(status int, body string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failMetrics = &fakePhaseFailure{status: status, body: body}
}

func (fw *fakeWorker) FailReport(status int, body string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failReport = &fakePhaseFailure{status: status, body: body}
}

func (fw *fakeWorker) SetReport(payload json.RawMessage) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.report = payload
}

type fakePhaseFailure struct {
	status int
	body   string
}

func (fw *fakeWorker) FailPhase(phase Phase, status int, body string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failPhases[phase] = fakePhaseFailure{status: status, body: body}
}

func (fw *fakeWorker) FailAssign(status int, body string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failAssign = &fakePhaseFailure{status: status, body: body}
}

func (fw *fakeWorker) HangStop() {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.hangStop = true
}

func (fw *fakeWorker) SetStatusAssignment(a worker.Assignment) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.statusAssignment = &a
}

func (fw *fakeWorker) BlockStatus(phase Phase) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.blockStatus[phase] = true
}

func (fw *fakeWorker) LagPhase(phase Phase) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.lagPhases == nil {
		fw.lagPhases = make(map[Phase]bool)
	}
	fw.lagPhases[phase] = true
}

func (fw *fakeWorker) KeepPhaseInProgress(phase Phase) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.activePhase[phase] = true
}

func (fw *fakeWorker) CompletePhaseAfter(phase Phase, d time.Duration) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.completeAfter == nil {
		fw.completeAfter = make(map[Phase]time.Duration)
	}
	fw.completeAfter[phase] = d
}

func (fw *fakeWorker) FailPhaseAfterAccept(phase Phase, message string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failAfterAccept[phase] = message
}

func (fw *fakeWorker) CancelWhenStatusPolled(cancel context.CancelFunc) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.cancelOnStatusPoll = cancel
}

func (fw *fakeWorker) CancelWhenMetricsPolled(cancel context.CancelFunc) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.cancelOnMetricsPoll = cancel
}

func (fw *fakeWorker) ObservedPhases() []Phase {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return append([]Phase(nil), fw.observed...)
}

func (fw *fakeWorker) Stopped() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.stopped
}

func (fw *fakeWorker) Assigned() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.assigned
}

func (fw *fakeWorker) Assignment() worker.Assignment {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.assignment
}

func (fw *fakeWorker) SawPhaseAttempt(phase Phase) bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for _, got := range fw.phaseAttempts {
		if got == phase {
			return true
		}
	}
	return false
}

func (fw *fakeWorker) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer secret" {
		http.Error(w, "missing auth", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/v1/assign":
		fw.handleAssign(w, r)
	case "/v1/phase/prepare":
		fw.handlePhase(w, PhasePrepare, worker.PhasePrepare)
	case "/v1/phase/connect":
		fw.handlePhase(w, PhaseConnect, worker.PhaseConnect)
	case "/v1/phase/warmup":
		fw.handlePhase(w, PhaseWarmup, worker.PhaseWarmup)
	case "/v1/phase/run":
		fw.handlePhase(w, PhaseRun, worker.PhaseRun)
	case "/v1/phase/cooldown":
		fw.handlePhase(w, PhaseCooldown, worker.PhaseCooldown)
	case "/v1/status":
		fw.handleStatus(w, r)
	case "/v1/metrics":
		fw.handleMetrics(w, r)
	case "/v1/report":
		fw.handleReport(w, r)
	case "/v1/stop":
		fw.handleStop(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (fw *fakeWorker) handleMetrics(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if cancel := fw.cancelOnMetricsPoll; cancel != nil {
		fw.cancelOnMetricsPoll = nil
		go cancel()
	}
	if failure := fw.failMetrics; failure != nil {
		fw.mu.Unlock()
		http.Error(w, failure.body, failure.status)
		return
	}
	snapshot := fw.metrics
	fw.mu.Unlock()
	writeRunTestJSON(w, snapshot)
}

func (fw *fakeWorker) handleReport(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if failure := fw.failReport; failure != nil {
		fw.mu.Unlock()
		http.Error(w, failure.body, failure.status)
		return
	}
	reportPayload := fw.report
	if len(reportPayload) == 0 {
		reportPayload = json.RawMessage(`{"worker_id":"` + fw.id + `"}`)
	}
	fw.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(reportPayload)
}

func (fw *fakeWorker) handleAssign(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if failure := fw.failAssign; failure != nil {
		fw.mu.Unlock()
		http.Error(w, failure.body, failure.status)
		return
	}
	fw.mu.Unlock()
	var assignment worker.Assignment
	if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fw.mu.Lock()
	fw.assigned = true
	fw.assignment = assignment
	fw.phase = worker.PhaseAssigned
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handlePhase(w http.ResponseWriter, phase Phase, workerPhase worker.Phase) {
	fw.mu.Lock()
	fw.phaseAttempts = append(fw.phaseAttempts, phase)
	if failure, ok := fw.failPhases[phase]; ok {
		fw.mu.Unlock()
		http.Error(w, failure.body, failure.status)
		return
	}
	fw.observed = append(fw.observed, phase)
	if !fw.lagPhases[phase] {
		fw.phase = workerPhase
	}
	if msg := fw.failAfterAccept[phase]; msg != "" {
		fw.activePhase[phase] = false
	}
	if d := fw.completeAfter[phase]; d > 0 {
		fw.activePhase[phase] = true
		fw.phaseAcceptedAt[phase] = time.Now()
	}
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handleStatus(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if cancel := fw.cancelOnStatusPoll; cancel != nil {
		fw.cancelOnStatusPoll = nil
		go cancel()
	}
	blocked := fw.blockStatus[Phase(fw.phase)]
	assignment := fw.assignment
	if fw.statusAssignment != nil {
		assignment = *fw.statusAssignment
	}
	status := worker.Status{Phase: fw.phase, Assignment: assignment}
	if msg := fw.failAfterAccept[Phase(fw.phase)]; msg != "" {
		status.LastError = msg
	} else if fw.activePhase[Phase(fw.phase)] {
		phase := Phase(fw.phase)
		if d := fw.completeAfter[phase]; d > 0 && !fw.phaseAcceptedAt[phase].IsZero() && time.Since(fw.phaseAcceptedAt[phase]) >= d {
			fw.activePhase[phase] = false
			status.CompletedPhase = fw.phase
		} else {
			status.ActivePhase = fw.phase
			status.CompletedPhase = previousFakePhase(Phase(fw.phase))
		}
	} else {
		status.CompletedPhase = fw.phase
	}
	fw.mu.Unlock()
	if blocked {
		<-r.Context().Done()
		return
	}
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handleStop(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if fw.hangStop {
		fw.mu.Unlock()
		<-r.Context().Done()
		return
	}
	fw.stopped = true
	fw.phase = worker.PhaseStopped
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func writeRunTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func previousFakePhase(phase Phase) worker.Phase {
	switch phase {
	case PhasePrepare:
		return worker.PhaseAssigned
	case PhaseConnect:
		return worker.PhasePrepare
	case PhaseWarmup:
		return worker.PhaseConnect
	case PhaseRun:
		return worker.PhaseWarmup
	case PhaseCooldown:
		return worker.PhaseRun
	default:
		return worker.Phase(phase)
	}
}

func fakeTargetOK() model.Target {
	return model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{"http://127.0.0.1:1"}},
		Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}
}

func fakeScenario() model.Scenario {
	rate, err := model.ParseRate("1/s")
	if err != nil {
		panic(err)
	}
	return model.Scenario{
		Version: "wkbench/v1",
		Run:     model.RunConfig{ID: "run-1", FailFast: true},
		Limits: model.LimitsConfig{Hard: model.HardLimitsConfig{
			MaxWorkerFailed:        -1,
			MaxConnectErrorRate:    -1,
			MaxSendackErrorRate:    -1,
			MaxRecvVerifyErrorRate: -1,
		}},
		Online: model.OnlineConfig{TotalUsers: 10},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name:        "group-hot",
			ChannelType: model.ChannelTypeGroup,
			Count:       1,
			Members:     model.MembersConfig{Count: 5},
		}}},
		Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{
			Name:           "group-send",
			ChannelRef:     "group-hot",
			RatePerChannel: rate,
		}}},
	}
}
