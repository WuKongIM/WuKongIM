package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	require.True(t, workers[0].Stopped())
	require.True(t, workers[1].Stopped())
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
	require.NotEmpty(t, assignment.AssignmentID)
	require.Equal(t, result.AssignmentID, assignment.AssignmentID)
	require.Equal(t, "a", assignment.WorkerID)
	require.Equal(t, "a", assignment.Plan.WorkerID)
	require.Contains(t, assignment.Plan.Profiles, "group-hot")
	require.Equal(t, result.Plan.ChannelOwners, assignment.ChannelOwners)
	require.Contains(t, assignment.ChannelOwners, "group-hot")
	require.Equal(t, fakeTargetOK().Gateway.TCP.Addrs, assignment.Target.Gateway.TCP.Addrs)
	require.Equal(t, "wkbench/v1", assignment.Scenario.Version)
}

func TestCoordinatorUsesNewAssignmentIDForSameRunRetry(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()

	first, err := coord.Run(context.Background(), scenario)
	require.NoError(t, err)
	second, err := coord.Run(context.Background(), scenario)
	require.NoError(t, err)

	require.NotEmpty(t, first.AssignmentID)
	require.NotEmpty(t, second.AssignmentID)
	require.NotEqual(t, first.AssignmentID, second.AssignmentID)
	require.Equal(t, second.AssignmentID, workers[0].Assignment().AssignmentID)
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

func TestCoordinatorRetriesExactRunStopUntilTerminalAcknowledged(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].FailStopAttempts(1)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].Stopped())
	require.GreaterOrEqual(t, workers[0].StopAttempts(), 2)
	require.Equal(t, []string{scenario.Run.ID, scenario.Run.ID}, workers[0].StopRunIDs())
}

func TestCoordinatorRetriesWhenFirstStopRequestHangs(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].HangStopAttempts(1)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  90 * time.Millisecond,
	})
	scenario := fakeScenario()

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].Stopped())
	require.GreaterOrEqual(t, workers[0].StopAttempts(), 2)
}

func TestCoordinatorStopsCurrentWorkerAfterAmbiguousAssignmentSuccess(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[1].DelayAssignResponseAfterAccept(50 * time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		HTTPClient:   &http.Client{Timeout: 10 * time.Millisecond},
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.True(t, workers[0].Stopped(), "previously acknowledged assignment was not stopped")
	require.True(t, workers[1].Stopped(), "ambiguous successful assignment was not stopped")
	require.Equal(t, []string{scenario.Run.ID}, workers[0].StopRunIDs())
	require.Equal(t, []string{scenario.Run.ID}, workers[1].StopRunIDs())
}

func TestCoordinatorAmbiguousAssignmentCleanupDoesNotStopDifferentRun(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].DelayAmbiguousAssignWithActiveRun("other-run", 50*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		HTTPClient:   &http.Client{Timeout: 10 * time.Millisecond},
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  30 * time.Millisecond,
	})
	scenario := fakeScenario()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.False(t, workers[0].Stopped(), "exact-run cleanup stopped a different active run")
	require.Equal(t, "other-run", workers[0].Assignment().RunID)
	require.NotEmpty(t, workers[0].StopRunIDs())
	for _, stopRunID := range workers[0].StopRunIDs() {
		require.Equal(t, scenario.Run.ID, stopRunID)
	}
	require.True(t, containsWorkerFailureReason(result.Report.WorkerFailures, "worker_stop_failed"))
	require.Empty(t, result.Report.WorkerMetrics)
	require.Empty(t, result.Report.WorkerReports)
}

func TestCoordinatorAssignmentCancellationWritesMinimalStopFailureReport(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].HangStop()
	ctx, cancel := context.WithCancel(context.Background())
	workers[1].CancelWhenAssigned(cancel)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  10 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(ctx, scenario)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, StatusCanceled, result.Status)
	require.False(t, workers[0].Stopped(), "unacknowledged stop must remain a terminal failure")
	require.False(t, workers[1].Assigned(), "the canceled assignment must not become active")
	require.Condition(t, func() bool {
		for _, failure := range result.Report.WorkerFailures {
			if failure.WorkerID == "a" && failure.Phase == "stop" && failure.ReasonCode == "worker_stop_failed" {
				return true
			}
		}
		return false
	}, "assignment cancellation must preserve terminal stop failure evidence")
	require.Empty(t, result.Report.WorkerMetrics)
	require.Empty(t, result.Report.WorkerReports)
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "diagnostic-summary.json"))
}

func TestCoordinatorAssignmentFailureWithUnconfirmedStopWritesMinimalReport(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].HangStop()
	workers[1].FailAssign(http.StatusInternalServerError, "assign exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
		StopTimeout:  30 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 2)
	require.True(t, containsWorkerFailureReason(result.Report.WorkerFailures, "worker_assignment_failed"))
	require.True(t, containsWorkerFailureReason(result.Report.WorkerFailures, "worker_stop_failed"))
	require.Empty(t, result.Report.WorkerMetrics)
	require.Empty(t, result.Report.WorkerReports)
	require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "diagnostic-summary.json"))
}

func TestAssignmentFailureClassificationMarksUnconfirmedStopIncomplete(t *testing.T) {
	classification := assignmentFailureClassification([]report.WorkerFailure{{ReasonCode: "worker_stop_failed"}})

	require.False(t, classification.EvidenceComplete)
	require.True(t, classification.HarnessInvalid)
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
	require.True(t, workers[0].Stopped(), "failed worker must be stopped before the terminal result")
	require.True(t, workers[1].Stopped(), "healthy worker must be stopped after all remaining phases were attempted")
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

func TestWorkloadReportClassificationMarksCollectionFailureIncomplete(t *testing.T) {
	classification := workloadReportClassification("", 1, 1)

	require.False(t, classification.EvidenceComplete)
	require.True(t, classification.HarnessInvalid)
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
	workers[0].SetAssignment(worker.Assignment{RunID: "run-1", AssignmentID: "generation-1", WorkerID: "a"})
	workers[0].SetReport(json.RawMessage(`{"worker_id":"worker-endpoint-id","report":{"run_id":"run-1","phase":"run","metrics":{"counters":{"send_success_total":1}}}}`))
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	_, workerReports, collectionFailures, failureDetails, err := coord.collectWorkerReports(context.Background(), "run-1", "generation-1")

	require.NoError(t, err)
	require.Empty(t, collectionFailures)
	require.Empty(t, failureDetails)
	require.Len(t, workerReports, 1)
	require.Equal(t, "a", workerReports[0].WorkerID)
	require.JSONEq(t, `{"run_id":"run-1","phase":"run","metrics":{"counters":{"send_success_total":1}}}`, string(workerReports[0].Report))
}

func TestCoordinatorCollectWorkerReportsRequiresAssignmentGeneration(t *testing.T) {
	coord := New(CoordinatorConfig{})

	_, _, _, _, err := coord.collectWorkerReports(context.Background(), "run-1", "")

	require.ErrorContains(t, err, "run_id and assignment_id")
}

func TestWorkerEvidencePathBindsRunAndAssignmentGeneration(t *testing.T) {
	path := workerEvidencePath("/v1/metrics", "run same", "generation/2")
	parsed, err := url.Parse(path)
	require.NoError(t, err)
	require.Equal(t, "/v1/metrics", parsed.Path)
	require.Equal(t, "run same", parsed.Query().Get("run_id"))
	require.Equal(t, "generation/2", parsed.Query().Get("assignment_id"))
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
		PollInterval: 100 * time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_timeout", result.Report.WorkerFailures[0].ReasonCode)
	require.Equal(t, "phase_completion", result.Report.WorkerFailures[0].Operation)
}

func TestCoordinatorWorkerStatusTimeoutIsIndependentFromPhasePollTimeout(t *testing.T) {
	coord := New(CoordinatorConfig{PollTimeout: 5 * time.Millisecond})

	require.Equal(t, defaultWorkerStatusTimeout, coord.workerStatusTimeout())
}

func TestCoordinatorPhaseBudgetSurvivesStatusRequestPastPollGrace(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].DelayStatus(PhaseRun, 20*time.Millisecond)
	workers[0].CompletePhaseAfter(PhaseRun, 35*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.Duration = 20 * time.Millisecond

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
}

func TestCoordinatorWorkerStatusDeadlineIsReportedAsPhaseTimeout(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].BlockStatus(PhaseConnect)
	coord := New(CoordinatorConfig{
		Workers:             workers.ClientConfigs(),
		Target:              fakeTargetOK(),
		Preflight:           preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval:        time.Millisecond,
		PollTimeout:         5 * time.Millisecond,
		WorkerStatusTimeout: 5 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_timeout", result.Report.WorkerFailures[0].ReasonCode)
	require.Equal(t, "worker_status", result.Report.WorkerFailures[0].Operation)
}

func TestCoordinatorLaterWorkerStatusDeadlineStillReportsWorkerStatus(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].KeepPhaseInProgress(PhasePrepare)
	workers[0].BlockStatusAfter(PhasePrepare, 1)
	coord := New(CoordinatorConfig{
		Workers:             workers.ClientConfigs(),
		Target:              fakeTargetOK(),
		Preflight:           preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval:        time.Millisecond,
		PollTimeout:         10 * time.Millisecond,
		WorkerStatusTimeout: 10 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_timeout", result.Report.WorkerFailures[0].ReasonCode)
	require.Equal(t, "worker_status", result.Report.WorkerFailures[0].Operation)
}

func TestCoordinatorDeadlineStraddleFinalActiveStatusReportsPhaseCompletion(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].KeepPhaseInProgress(PhasePrepare)
	workers[0].BlockOneStatusAfter(PhasePrepare, 1)
	coord := New(CoordinatorConfig{
		Workers:             workers.ClientConfigs(),
		Target:              fakeTargetOK(),
		Preflight:           preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval:        time.Millisecond,
		PollTimeout:         10 * time.Millisecond,
		WorkerStatusTimeout: 10 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_timeout", result.Report.WorkerFailures[0].ReasonCode)
	require.Equal(t, "phase_completion", result.Report.WorkerFailures[0].Operation)
}

func TestCoordinatorNonFailFastTimeoutStopsActiveWorker(t *testing.T) {
	runner := newCoordinatorBlockingPrepareRunner()
	workerServer := httptest.NewServer(worker.NewServer(worker.Config{
		ControlToken:   "secret",
		WorkloadRunner: runner,
	}))
	t.Cleanup(workerServer.Close)
	t.Cleanup(runner.release)
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
		StopTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = false

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	require.Equal(t, "phase_timeout", result.Report.WorkerFailures[0].ReasonCode)
	require.True(t, runner.waitCanceled(100*time.Millisecond), "terminal coordinator result left the worker phase running")
}

func TestCoordinatorFailFastTimeoutWaitsForWorkerTeardown(t *testing.T) {
	runner := newCoordinatorDelayedStopRunner()
	t.Cleanup(runner.release)
	workerControl := worker.NewServer(worker.Config{
		ControlToken:   "secret",
		WorkloadRunner: runner,
	})
	workerServer := httptest.NewServer(workerControl)
	t.Cleanup(workerServer.Close)
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: 100 * time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
		StopTimeout:  time.Second,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true

	type runOutcome struct {
		result RunResult
		err    error
	}
	runDone := make(chan runOutcome, 1)
	go func() {
		result, err := coord.Run(context.Background(), scenario)
		runDone <- runOutcome{result: result, err: err}
	}()
	require.True(t, runner.waitCanceled(time.Second), "terminal stop did not cancel the active fail-fast phase")
	select {
	case outcome := <-runDone:
		t.Fatalf("coordinator returned before worker teardown: status=%s err=%v", outcome.result.Status, outcome.err)
	case <-time.After(20 * time.Millisecond):
	}
	require.False(t, runner.waitEnded(20*time.Millisecond), "runner teardown ran before the active phase exited")

	runner.release()
	select {
	case outcome := <-runDone:
		require.Error(t, outcome.err)
		require.Equal(t, StatusWorkerFailed, outcome.result.Status)
		require.Len(t, outcome.result.Report.WorkerFailures, 1)
		require.Equal(t, "phase_timeout", outcome.result.Report.WorkerFailures[0].ReasonCode)
	case <-time.After(time.Second):
		t.Fatal("coordinator did not return after worker teardown completed")
	}
	require.True(t, runner.waitEnded(time.Second), "terminal stop did not end assignment resources")
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

func TestCoordinatorWarmupPollTimeoutIncludesOperationTail(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].CompletePhaseAfter(PhaseWarmup, 35*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.Warmup = 20 * time.Millisecond
	scenario.Messages.Traffic[0].AckTimeout = 20 * time.Millisecond

	result, err := coord.Run(context.Background(), scenario)

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.True(t, workers[0].SawPhaseAttempt(PhaseRun))
}

func TestCoordinatorRunPollTimeoutIncludesOperationTail(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].CompletePhaseAfter(PhaseRun, 35*time.Millisecond)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.Duration = 20 * time.Millisecond
	scenario.Messages.Traffic[0].RecvTimeout = 20 * time.Millisecond

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
	require.Contains(t, err.Error(), "worker a stop failed")
	require.True(t, workers[1].Stopped(), "healthy worker should still receive stop")
	require.Condition(t, func() bool {
		for _, failure := range result.Report.WorkerFailures {
			if failure.WorkerID == "a" && failure.Phase == "stop" && failure.ReasonCode == "worker_stop_failed" {
				return true
			}
		}
		return false
	}, "stop acknowledgement failure must survive in bounded diagnostic evidence")
	require.Empty(t, result.Report.WorkerMetrics, "unconfirmed stop must not read moving worker metrics")
	require.Empty(t, result.Report.WorkerReports, "unconfirmed stop must not read moving worker reports")
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestCoordinatorCanceledRunWritesMinimalStopFailureReport(t *testing.T) {
	runner := newCoordinatorDelayedStopRunner()
	workerControl := worker.NewServer(worker.Config{ControlToken: "secret", WorkloadRunner: runner})
	workerServer := httptest.NewServer(workerControl)
	t.Cleanup(workerServer.Close)
	t.Cleanup(runner.release)
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
		StopTimeout:  10 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	type runOutcome struct {
		result RunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, err := coord.Run(ctx, scenario)
		done <- runOutcome{result: result, err: err}
	}()
	require.True(t, runner.waitStarted(time.Second), "prepare hook did not start")
	cancel()

	select {
	case outcome := <-done:
		require.ErrorIs(t, outcome.err, context.Canceled)
		require.Equal(t, StatusCanceled, outcome.result.Status)
		require.Len(t, outcome.result.Report.WorkerFailures, 1)
		require.Equal(t, "worker_stop_failed", outcome.result.Report.WorkerFailures[0].ReasonCode)
		require.Empty(t, outcome.result.Report.WorkerMetrics)
		require.FileExists(t, filepath.Join(scenario.Run.ReportDir, "diagnostic-summary.json"))
	case <-time.After(time.Second):
		t.Fatal("canceled coordinator did not return after bounded stop timeout")
	}
	runner.release()
	require.True(t, runner.waitEnded(time.Second), "background stop did not finish after canceled coordinator returned")
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

func TestCoordinatorPreservesSafeWorkerFailureOperation(t *testing.T) {
	workerServer := httptest.NewServer(worker.NewServer(worker.Config{
		ControlToken: "secret",
		WorkloadRunner: &tcpSourceFailureRunner{connectErr: &benchworkload.SessionError{
			UID:       "secret-user",
			Operation: "group sendack",
			Err:       io.EOF,
		}},
	}))
	defer workerServer.Close()
	reportDir := t.TempDir()
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = reportDir

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	failureJSON, marshalErr := json.Marshal(result.Report.WorkerFailures[0])
	require.NoError(t, marshalErr)
	var failure map[string]any
	require.NoError(t, json.Unmarshal(failureJSON, &failure))
	require.Equal(t, "group_sendack", failure["operation"])

	data, readErr := os.ReadFile(filepath.Join(reportDir, "diagnostic-summary.json"))
	require.NoError(t, readErr)
	var summary map[string]any
	require.NoError(t, json.Unmarshal(data, &summary))
	failedWorkers := summary["failed_workers"].([]any)
	diagnosticFailure := failedWorkers[0].(map[string]any)
	require.Equal(t, "group_sendack", diagnosticFailure["operation"])
	require.NotContains(t, string(data), "secret-user")
}

func TestCoordinatorPreservesAsyncWorkerFailureOperation(t *testing.T) {
	workerServer := httptest.NewServer(worker.NewServer(worker.Config{
		ControlToken: "secret",
		WorkloadRunner: &delayedConnectFailureRunner{
			delay: 50 * time.Millisecond,
			err: &benchworkload.SessionError{
				UID:       "secret-user",
				Operation: "person recv",
				Err:       io.EOF,
			},
		},
	}))
	defer workerServer.Close()
	coord := New(CoordinatorConfig{
		Workers: []model.Worker{{ID: "a", Addr: workerServer.URL, Weight: 1, ControlToken: "secret"}},
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return nil
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  250 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.ReportDir = t.TempDir()

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Len(t, result.Report.WorkerFailures, 1)
	failureJSON, marshalErr := json.Marshal(result.Report.WorkerFailures[0])
	require.NoError(t, marshalErr)
	require.Contains(t, string(failureJSON), `"operation":"person_recv"`)
	data, readErr := os.ReadFile(filepath.Join(scenario.Run.ReportDir, "diagnostic-summary.json"))
	require.NoError(t, readErr)
	require.Contains(t, string(data), `"operation": "person_recv"`)
	require.NotContains(t, string(data), "secret-user")
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

type delayedConnectFailureRunner struct {
	delay time.Duration
	err   error
}

type coordinatorBlockingPrepareRunner struct {
	releaseCh chan struct{}
	canceled  chan struct{}
	once      sync.Once
	cancel    sync.Once
}

type coordinatorDelayedStopRunner struct {
	started  chan struct{}
	canceled chan struct{}
	ended    chan struct{}
	releaseC chan struct{}
	start    sync.Once
	cancel   sync.Once
	end      sync.Once
	releaseO sync.Once
}

func newCoordinatorDelayedStopRunner() *coordinatorDelayedStopRunner {
	return &coordinatorDelayedStopRunner{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		ended:    make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *coordinatorDelayedStopRunner) Prepare(ctx context.Context, _ worker.Assignment) error {
	r.start.Do(func() { close(r.started) })
	<-ctx.Done()
	r.cancel.Do(func() { close(r.canceled) })
	<-r.releaseC
	return ctx.Err()
}

func (r *coordinatorDelayedStopRunner) Connect(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorDelayedStopRunner) Warmup(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorDelayedStopRunner) Run(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorDelayedStopRunner) Cooldown(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorDelayedStopRunner) EndAssignment(worker.Assignment) error {
	r.end.Do(func() { close(r.ended) })
	return nil
}

func (r *coordinatorDelayedStopRunner) waitCanceled(timeout time.Duration) bool {
	select {
	case <-r.canceled:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *coordinatorDelayedStopRunner) waitStarted(timeout time.Duration) bool {
	select {
	case <-r.started:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *coordinatorDelayedStopRunner) waitEnded(timeout time.Duration) bool {
	select {
	case <-r.ended:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *coordinatorDelayedStopRunner) release() {
	r.releaseO.Do(func() { close(r.releaseC) })
}

func newCoordinatorBlockingPrepareRunner() *coordinatorBlockingPrepareRunner {
	return &coordinatorBlockingPrepareRunner{
		releaseCh: make(chan struct{}),
		canceled:  make(chan struct{}),
	}
}

func (r *coordinatorBlockingPrepareRunner) Prepare(ctx context.Context, _ worker.Assignment) error {
	select {
	case <-r.releaseCh:
		return nil
	case <-ctx.Done():
		r.cancel.Do(func() { close(r.canceled) })
		return ctx.Err()
	}
}

func (r *coordinatorBlockingPrepareRunner) Connect(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorBlockingPrepareRunner) Warmup(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorBlockingPrepareRunner) Run(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorBlockingPrepareRunner) Cooldown(context.Context, worker.Assignment) error {
	return nil
}

func (r *coordinatorBlockingPrepareRunner) waitCanceled(timeout time.Duration) bool {
	select {
	case <-r.canceled:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *coordinatorBlockingPrepareRunner) release() {
	r.once.Do(func() { close(r.releaseCh) })
}

func (r *delayedConnectFailureRunner) Prepare(context.Context, worker.Assignment) error { return nil }
func (r *delayedConnectFailureRunner) Connect(ctx context.Context, _ worker.Assignment) error {
	timer := time.NewTimer(r.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return r.err
	}
}
func (r *delayedConnectFailureRunner) Warmup(context.Context, worker.Assignment) error { return nil }
func (r *delayedConnectFailureRunner) Run(context.Context, worker.Assignment) error    { return nil }
func (r *delayedConnectFailureRunner) Cooldown(context.Context, worker.Assignment) error {
	return nil
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
			id:               string(rune('a' + i)),
			phase:            worker.PhaseIdle,
			failPhases:       make(map[Phase]fakePhaseFailure),
			blockStatus:      make(map[Phase]bool),
			blockStatusAfter: make(map[Phase]int),
			blockStatusOnce:  make(map[Phase]int),
			statusCalls:      make(map[Phase]int),
			activePhase:      make(map[Phase]bool),
			completeAfter:    make(map[Phase]time.Duration),
			phaseAcceptedAt:  make(map[Phase]time.Time),
			statusDelay:      make(map[Phase]time.Duration),
			failAfterAccept:  make(map[Phase]string),
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
	stopAttempts        int
	stopFailures        int
	stopHangs           int
	stopRunIDs          []string
	stopAssignmentIDs   []string
	assignResponseDelay time.Duration
	ambiguousActiveRun  string
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
	blockStatusAfter    map[Phase]int
	blockStatusOnce     map[Phase]int
	statusCalls         map[Phase]int
	activePhase         map[Phase]bool
	completeAfter       map[Phase]time.Duration
	phaseAcceptedAt     map[Phase]time.Time
	statusDelay         map[Phase]time.Duration
	failAfterAccept     map[Phase]string
	cancelOnAssign      context.CancelFunc
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

func (fw *fakeWorker) SetAssignment(assignment worker.Assignment) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.assignment = assignment
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

func (fw *fakeWorker) DelayAssignResponseAfterAccept(delay time.Duration) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.assignResponseDelay = delay
}

func (fw *fakeWorker) DelayAmbiguousAssignWithActiveRun(runID string, delay time.Duration) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.ambiguousActiveRun = runID
	fw.assignResponseDelay = delay
}

func (fw *fakeWorker) CancelWhenAssigned(cancel context.CancelFunc) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.cancelOnAssign = cancel
}

func (fw *fakeWorker) HangStop() {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.hangStop = true
}

func (fw *fakeWorker) FailStopAttempts(count int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.stopFailures = count
}

func (fw *fakeWorker) HangStopAttempts(count int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.stopHangs = count
}

func (fw *fakeWorker) StopAttempts() int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.stopAttempts
}

func (fw *fakeWorker) StopRunIDs() []string {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return append([]string(nil), fw.stopRunIDs...)
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

func (fw *fakeWorker) BlockStatusAfter(phase Phase, successfulCalls int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.blockStatusAfter[phase] = successfulCalls
}

func (fw *fakeWorker) BlockOneStatusAfter(phase Phase, successfulCalls int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.blockStatusOnce[phase] = successfulCalls + 1
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

func (fw *fakeWorker) DelayStatus(phase Phase, d time.Duration) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.statusDelay[phase] = d
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
	case "/v1/prepare/channels":
		fw.handlePrepareChannels(w, r)
	case "/v1/phase/prepare":
		fw.handlePhase(w, r, PhasePrepare, worker.PhasePrepare)
	case "/v1/phase/connect":
		fw.handlePhase(w, r, PhaseConnect, worker.PhaseConnect)
	case "/v1/phase/warmup":
		fw.handlePhase(w, r, PhaseWarmup, worker.PhaseWarmup)
	case "/v1/phase/run":
		fw.handlePhase(w, r, PhaseRun, worker.PhaseRun)
	case "/v1/phase/cooldown":
		fw.handlePhase(w, r, PhaseCooldown, worker.PhaseCooldown)
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
	if !fw.validateEvidenceRequest(w, r) {
		return
	}
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
	if !fw.validateEvidenceRequest(w, r) {
		return
	}
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

func (fw *fakeWorker) validateEvidenceRequest(w http.ResponseWriter, r *http.Request) bool {
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	assignmentID := strings.TrimSpace(r.URL.Query().Get("assignment_id"))
	if runID == "" || assignmentID == "" {
		http.Error(w, "run_id and assignment_id are required", http.StatusBadRequest)
		return false
	}
	fw.mu.Lock()
	assignment := fw.assignment
	fw.mu.Unlock()
	if assignment.RunID != runID || assignment.AssignmentID != assignmentID {
		http.Error(w, "active assignment conflict", http.StatusConflict)
		return false
	}
	return true
}

func (fw *fakeWorker) handleAssign(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if cancel := fw.cancelOnAssign; cancel != nil {
		fw.cancelOnAssign = nil
		fw.mu.Unlock()
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
		return
	}
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
	if fw.ambiguousActiveRun != "" {
		assignment.RunID = fw.ambiguousActiveRun
	}
	fw.assignment = assignment
	fw.phase = worker.PhaseAssigned
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	delay := fw.assignResponseDelay
	fw.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handlePrepareChannels(w http.ResponseWriter, r *http.Request) {
	request, ok := fw.decodeRunRequest(w, r)
	if !ok {
		return
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.assignment.RunID != request.RunID || fw.assignment.AssignmentID != request.AssignmentID {
		http.Error(w, "active assignment conflict", http.StatusConflict)
		return
	}
	writeRunTestJSON(w, worker.Status{Phase: fw.phase, Assignment: fw.assignment})
}

func (fw *fakeWorker) handlePhase(w http.ResponseWriter, r *http.Request, phase Phase, workerPhase worker.Phase) {
	request, ok := fw.decodeRunRequest(w, r)
	if !ok {
		return
	}
	fw.mu.Lock()
	if fw.assignment.RunID != request.RunID || fw.assignment.AssignmentID != request.AssignmentID {
		fw.mu.Unlock()
		http.Error(w, "active assignment conflict", http.StatusConflict)
		return
	}
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

func (fw *fakeWorker) decodeRunRequest(w http.ResponseWriter, r *http.Request) (worker.RunRequest, bool) {
	var request worker.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return worker.RunRequest{}, false
	}
	if request.RunID == "" || request.AssignmentID == "" {
		http.Error(w, "run_id and assignment_id are required", http.StatusBadRequest)
		return worker.RunRequest{}, false
	}
	return request, true
}

func (fw *fakeWorker) handleStatus(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if cancel := fw.cancelOnStatusPoll; cancel != nil {
		fw.cancelOnStatusPoll = nil
		go cancel()
	}
	phase := Phase(fw.phase)
	fw.statusCalls[phase]++
	blockedOnce := fw.blockStatusOnce[phase] > 0 && fw.statusCalls[phase] == fw.blockStatusOnce[phase]
	if blockedOnce {
		delete(fw.blockStatusOnce, phase)
	}
	blocked := blockedOnce || fw.blockStatus[phase] || fw.blockStatusAfter[phase] > 0 && fw.statusCalls[phase] > fw.blockStatusAfter[phase]
	assignment := fw.assignment
	if fw.statusAssignment != nil {
		assignment = *fw.statusAssignment
	}
	status := worker.Status{Phase: fw.phase, Assignment: assignment}
	if msg := fw.failAfterAccept[Phase(fw.phase)]; msg != "" {
		status.LastError = msg
	} else if fw.activePhase[phase] {
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
	delay := fw.statusDelay[phase]
	fw.mu.Unlock()
	if blocked {
		<-r.Context().Done()
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handleStop(w http.ResponseWriter, r *http.Request) {
	var request worker.StopRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fw.mu.Lock()
	fw.stopAttempts++
	fw.stopRunIDs = append(fw.stopRunIDs, request.RunID)
	fw.stopAssignmentIDs = append(fw.stopAssignmentIDs, request.AssignmentID)
	if fw.stopFailures > 0 {
		fw.stopFailures--
		fw.mu.Unlock()
		http.Error(w, "stop response lost", http.StatusServiceUnavailable)
		return
	}
	if fw.stopHangs > 0 {
		fw.stopHangs--
		fw.mu.Unlock()
		<-r.Context().Done()
		return
	}
	if fw.hangStop {
		fw.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
		return
	}
	if fw.assignment.RunID != request.RunID || fw.assignment.AssignmentID != request.AssignmentID {
		fw.mu.Unlock()
		http.Error(w, "active run conflict", http.StatusConflict)
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
