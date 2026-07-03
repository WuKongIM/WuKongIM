package devsim

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestRunnerWaitsForReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &fakeProbe{failuresBeforeReady: 2}
	workload := &fakeWorkload{runHook: func(context.Context, worker.Assignment) error {
		cancel()
		return context.Canceled
	}}
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   NewStatus("dev-sim-run"),
		Probe:    probe,
		Workload: workload,
		Sleep:    noSleep,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, 3, probe.calls)
	require.Equal(t, []string{"prepare", "connect", "warmup", "run", "cooldown"}, workload.calls)
}

func TestRunnerClearsTransientReadinessErrorBeforePrepare(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := NewStatus("dev-sim-run")
	var lastErrorAtPrepare string
	workload := &fakeWorkload{
		prepareHook: func(context.Context, worker.Assignment) error {
			lastErrorAtPrepare = status.Snapshot().LastError
			cancel()
			return context.Canceled
		},
	}
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{failuresBeforeReady: 1},
		Workload: workload,
		Sleep:    noSleep,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Empty(t, lastErrorAtPrepare)
}

func TestRunnerRunsWarmupBeforeTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testRunnerConfig()
	cfg.Traffic.Warmup = time.Millisecond
	workload := &fakeWorkload{runHook: func(context.Context, worker.Assignment) error {
		cancel()
		return context.Canceled
	}}
	runner := NewRunner(RunnerConfig{
		Config:   cfg,
		RunID:    "dev-sim-run",
		Status:   NewStatus("dev-sim-run"),
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep:    noSleep,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"prepare", "connect", "warmup", "run", "cooldown"}, workload.calls)
}

func TestRunnerUsesWarmupMetricsAsCounterBaseline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testRunnerConfig()
	cfg.Traffic.Warmup = time.Millisecond
	workload := &fakeWorkload{metrics: metrics.SnapshotData{Counters: map[string]uint64{}}}
	workload.warmupHook = func(context.Context, worker.Assignment) error {
		workload.metrics.Counters["group_send_success_total"] = 10
		return nil
	}
	workload.runHook = func(context.Context, worker.Assignment) error {
		workload.metrics.Counters["group_send_success_total"] = 12
		return nil
	}
	status := NewStatus("dev-sim-run")
	runner := NewRunner(RunnerConfig{
		Config:   cfg,
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, uint64(2), status.Snapshot().MessagesSent)
}

func TestRunnerKeepsConnectionsAfterRuntimeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workload := &fakeWorkload{}
	runtimeErr := errors.New("gateway unavailable")
	runCalls := 0
	workload.runHook = func(context.Context, worker.Assignment) error {
		runCalls++
		if runCalls == 1 {
			return runtimeErr
		}
		cancel()
		return context.Canceled
	}
	status := NewStatus("dev-sim-run")
	sawRetryError := false
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep: func(ctx context.Context, d time.Duration) error {
			if strings.Contains(status.Snapshot().LastError, "gateway unavailable") {
				sawRetryError = true
			}
			return noSleep(ctx, d)
		},
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"prepare", "connect", "warmup", "run", "reset", "run", "cooldown"}, workload.calls)
	require.Equal(t, []string{"sim-msg"}, workload.preparePrefixes)
	require.Equal(t, []string{"sim-msg-r1"}, workload.resetPrefixes)
	require.True(t, sawRetryError)
}

func TestRunnerStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workload := &fakeWorkload{runHook: func(ctx context.Context, _ worker.Assignment) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	status := NewStatus("dev-sim-run")
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep:    noSleep,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, StateStopped, status.Snapshot().State)
	require.Contains(t, workload.calls, "cooldown")
}

func TestNewRunnerUsesDefaultWorkload(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		Config: testRunnerConfig(),
		RunID:  "dev-sim-run",
		Status: NewStatus("dev-sim-run"),
		Probe:  &fakeProbe{},
	})

	require.NotNil(t, runner.workload)
}

func TestRunnerCopiesWorkloadMetricsToStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workload := &fakeWorkload{metrics: metrics.SnapshotData{Counters: map[string]uint64{}}}
	workload.runHook = func(context.Context, worker.Assignment) error {
		workload.metrics.Counters["person_send_success_total"] = 2
		workload.metrics.Counters["group_send_success_total"] = 3
		workload.metrics.Counters["group_recv_error_total"] = 1
		return nil
	}
	status := NewStatus("dev-sim-run")
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, uint64(5), status.Snapshot().MessagesSent)
	require.Equal(t, uint64(1), status.Snapshot().RecvErrors)
}

func TestRunnerSamplesLiveConnectionStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workload := &fakeWorkloadWithConnectionStatus{
		fakeWorkload: fakeWorkload{
			runHook: func(context.Context, worker.Assignment) error {
				return nil
			},
		},
		activeUsers:      17,
		reconnectedUsers: 4,
	}
	status := NewStatus("dev-sim-run")
	runner := NewRunner(RunnerConfig{
		Config:   testRunnerConfig(),
		RunID:    "dev-sim-run",
		Status:   status,
		Probe:    &fakeProbe{},
		Workload: workload,
		Sleep: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	require.Equal(t, 17, status.Snapshot().ActiveUsers)
	require.Equal(t, uint64(4), status.Snapshot().ReconnectedUsers)
}

type fakeProbe struct {
	calls               int
	failuresBeforeReady int
}

func (p *fakeProbe) CheckReady(context.Context) error {
	p.calls++
	if p.calls <= p.failuresBeforeReady {
		return errors.New("not ready")
	}
	return nil
}

type fakeWorkload struct {
	calls           []string
	preparePrefixes []string
	resetPrefixes   []string
	prepareHook     func(context.Context, worker.Assignment) error
	warmupHook      func(context.Context, worker.Assignment) error
	runHook         func(context.Context, worker.Assignment) error
	metrics         metrics.SnapshotData
}

func (w *fakeWorkload) Prepare(ctx context.Context, assignment worker.Assignment) error {
	w.calls = append(w.calls, "prepare")
	w.preparePrefixes = append(w.preparePrefixes, assignment.Scenario.Identity.ClientMsgPrefix)
	if w.prepareHook != nil {
		return w.prepareHook(ctx, assignment)
	}
	return nil
}

type fakeWorkloadWithConnectionStatus struct {
	fakeWorkload
	activeUsers      int
	reconnectedUsers uint64
}

func (w *fakeWorkloadWithConnectionStatus) ConnectionStatus() (int, uint64) {
	return w.activeUsers, w.reconnectedUsers
}

func (w *fakeWorkload) Connect(context.Context, worker.Assignment) error {
	w.calls = append(w.calls, "connect")
	return nil
}

func (w *fakeWorkload) ResetTraffic(assignment worker.Assignment) error {
	w.calls = append(w.calls, "reset")
	w.resetPrefixes = append(w.resetPrefixes, assignment.Scenario.Identity.ClientMsgPrefix)
	return nil
}

func (w *fakeWorkload) Warmup(ctx context.Context, assignment worker.Assignment) error {
	w.calls = append(w.calls, "warmup")
	if w.warmupHook != nil {
		return w.warmupHook(ctx, assignment)
	}
	return nil
}

func (w *fakeWorkload) Run(ctx context.Context, assignment worker.Assignment) error {
	w.calls = append(w.calls, "run")
	if w.runHook != nil {
		return w.runHook(ctx, assignment)
	}
	return nil
}

func (w *fakeWorkload) Cooldown(context.Context, worker.Assignment) error {
	w.calls = append(w.calls, "cooldown")
	return nil
}

func (w *fakeWorkload) MetricsSnapshot() metrics.SnapshotData {
	return w.metrics
}

func testRunnerConfig() Config {
	cfg := defaultConfig()
	cfg.Target.APIAddrs = []string{"http://127.0.0.1:5001"}
	cfg.Target.GatewayTCPAddrs = []string{"127.0.0.1:5100"}
	cfg.Traffic.Window = time.Millisecond
	cfg.Traffic.Cooldown = time.Millisecond
	cfg.Retry.ReadinessTimeout = time.Second
	cfg.Retry.RestartBackoff = time.Millisecond
	cfg.Online.ConnectRate = model.Rate{PerSecond: 1000}
	return cfg
}

func noSleep(context.Context, time.Duration) error { return nil }
