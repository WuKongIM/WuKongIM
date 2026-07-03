package devsim

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const devSimOperationTimeout = 30 * time.Second

// TargetProbe checks whether the black-box target is ready for simulator traffic.
type TargetProbe interface {
	// CheckReady verifies health, readiness, and benchmark API capabilities.
	CheckReady(ctx context.Context) error
}

// SleepFunc pauses between readiness checks and retry loops.
type SleepFunc func(context.Context, time.Duration) error

// RunnerConfig controls a long-running development simulator supervisor.
type RunnerConfig struct {
	// Config is the compact simulator configuration.
	Config Config
	// RunID is the deterministic run identifier used in generated traffic.
	RunID string
	// Status is updated as the simulator progresses.
	Status *Status
	// Probe checks black-box target readiness.
	Probe TargetProbe
	// Workload executes the derived wkbench assignment.
	Workload worker.WorkloadRunner
	// Sleep overrides timer behavior for tests.
	Sleep SleepFunc
}

// Runner supervises target readiness, connection setup, traffic windows, and retries.
type Runner struct {
	cfg      Config
	runID    string
	status   *Status
	probe    TargetProbe
	workload worker.WorkloadRunner
	sleep    SleepFunc
	last     metrics.SnapshotData
}

// NewRunner creates a dev-sim supervisor.
func NewRunner(cfg RunnerConfig) *Runner {
	runID := strings.TrimSpace(cfg.RunID)
	if runID == "" {
		runID = NewRunID(time.Now())
	}
	status := cfg.Status
	if status == nil {
		status = NewStatus(runID)
	}
	status.SetConfig(cfg.Config.Snapshot())
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	probe := cfg.Probe
	if probe == nil {
		inputs, _ := cfg.Config.BuildBenchInputs(runID)
		probe = NewTargetProbe(inputs.Target)
	}
	workload := cfg.Workload
	if workload == nil {
		workload = worker.NewDefaultWorkloadRunner(devSimClientFactory)
	}
	return &Runner{
		cfg:      cfg.Config,
		runID:    runID,
		status:   status,
		probe:    probe,
		workload: workload,
		sleep:    sleep,
	}
}

func devSimClientFactory(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error) {
	return benchwkproto.NewClient(benchwkproto.ClientConfig{
		Addr:             addr,
		Token:            user.Token,
		OperationTimeout: devSimOperationTimeout,
	})
}

// Run starts the status server and simulator supervisor until ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	runID := NewRunID(time.Now())
	status := NewStatus(runID)
	server := NewStatusServer(cfg.Status.Listen, status)
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(serverCtx)
	}()
	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("status server: %w", err)
		}
	case <-time.After(50 * time.Millisecond):
	}

	runErr := NewRunner(RunnerConfig{Config: cfg, RunID: runID, Status: status}).Run(ctx)
	stopServer()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	_ = server.Close(shutdownCtx)
	cancel()
	select {
	case err := <-serverErr:
		if runErr == nil && err != nil {
			return fmt.Errorf("status server: %w", err)
		}
	default:
	}
	return runErr
}

// Run executes the simulator until ctx is canceled or initial readiness times out.
func (r *Runner) Run(ctx context.Context) error {
	inputs, err := r.cfg.BuildBenchInputs(r.runID)
	if err != nil {
		r.status.SetLastError(err.Error())
		r.status.SetState(StateStopped)
		return err
	}
	attempt := 0
	for {
		assignment, err := assignmentFromInputs(inputs, attempt)
		if err != nil {
			r.status.SetLastError(err.Error())
			r.status.SetState(StateStopped)
			return err
		}
		if err := r.waitReady(ctx); err != nil {
			if ctx.Err() != nil {
				r.status.SetState(StateStopped)
				return nil
			}
			r.status.SetLastError(err.Error())
			r.status.SetState(StateStopped)
			return err
		}
		if err := r.prepareConnect(ctx, assignment); err != nil {
			if ctx.Err() != nil {
				r.stop(assignment)
				return nil
			}
			r.status.SetLastError(err.Error())
			r.status.SetState(StateRetrying)
			if err := r.sleep(ctx, r.cfg.Retry.RestartBackoff); err != nil {
				r.stop(assignment)
				return nil
			}
			attempt++
			continue
		}
		r.status.SetRunning(r.cfg.Online.TotalUsers, r.cfg.Profiles.PersonChannels, r.cfg.Profiles.GroupChannels)
		r.updateConnectionStatus()
		for {
			if err := r.runWorkload(ctx, assignment); err != nil {
				if ctx.Err() != nil {
					r.stop(assignment)
					return nil
				}
				cause := err
				r.status.SetLastError(err.Error())
				r.status.SetState(StateRetrying)
				if err := r.sleep(ctx, r.cfg.Retry.RestartBackoff); err != nil {
					r.stop(assignment)
					return nil
				}
				attempt++
				nextAssignment, err := assignmentFromInputs(inputs, attempt)
				if err != nil {
					r.status.SetLastError(err.Error())
					r.stop(assignment)
					return err
				}
				if recoverer, ok := r.workload.(worker.TrafficRecoverer); ok {
					if err := recoverer.RecoverTraffic(ctx, nextAssignment, cause); err != nil {
						r.status.SetLastError(err.Error())
						r.cooldown(assignment)
						break
					}
					assignment = nextAssignment
					r.status.SetRunning(r.cfg.Online.TotalUsers, r.cfg.Profiles.PersonChannels, r.cfg.Profiles.GroupChannels)
					r.updateConnectionStatus()
					continue
				}
				if resetter, ok := r.workload.(worker.TrafficResetter); ok {
					if err := resetter.ResetTraffic(nextAssignment); err != nil {
						r.status.SetLastError(err.Error())
						r.cooldown(assignment)
						break
					}
					assignment = nextAssignment
					r.status.SetRunning(r.cfg.Online.TotalUsers, r.cfg.Profiles.PersonChannels, r.cfg.Profiles.GroupChannels)
					r.updateConnectionStatus()
					continue
				}
				r.cooldown(assignment)
				break
			}
			if err := r.sleep(ctx, r.cfg.Traffic.Cooldown); err != nil {
				r.stop(assignment)
				return nil
			}
		}
	}
}

func (r *Runner) runWorkload(ctx context.Context, assignment worker.Assignment) error {
	done := make(chan error, 1)
	go func() {
		done <- r.workload.Run(ctx, assignment)
	}()
	ticker := time.NewTicker(r.counterInterval())
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			r.updateRuntimeStatus()
			return err
		case <-ticker.C:
			r.updateRuntimeStatus()
		case <-ctx.Done():
			r.updateRuntimeStatus()
			return ctx.Err()
		}
	}
}

func (r *Runner) counterInterval() time.Duration {
	if r.cfg.Traffic.Window > 0 {
		return r.cfg.Traffic.Window
	}
	return time.Second
}

func (r *Runner) updateRuntimeStatus() {
	r.updateCounters()
	r.updateConnectionStatus()
}

func (r *Runner) updateCounters() {
	reporter, ok := r.workload.(worker.MetricsReporter)
	if !ok {
		return
	}
	snapshot := reporter.MetricsSnapshot()
	r.status.AddMessagesSent(counterDelta(snapshot, r.last, "person_send_success_total", "group_send_success_total"))
	r.status.AddSendErrors(counterDelta(snapshot, r.last, "person_send_error_total", "group_send_error_total"))
	r.status.AddRecvErrors(counterDelta(snapshot, r.last, "person_recv_error_total", "group_recv_error_total"))
	r.last = cloneMetricsSnapshot(snapshot)
}

func (r *Runner) updateConnectionStatus() {
	reporter, ok := r.workload.(worker.ConnectionStatusReporter)
	if !ok {
		return
	}
	activeUsers, reconnectedUsers := reporter.ConnectionStatus()
	r.status.SetConnectionStats(activeUsers, reconnectedUsers)
}

func (r *Runner) captureCounterBaseline() {
	reporter, ok := r.workload.(worker.MetricsReporter)
	if !ok {
		return
	}
	r.last = cloneMetricsSnapshot(reporter.MetricsSnapshot())
}

func cloneMetricsSnapshot(snapshot metrics.SnapshotData) metrics.SnapshotData {
	out := snapshot
	if snapshot.Counters != nil {
		out.Counters = make(map[string]uint64, len(snapshot.Counters))
		for key, value := range snapshot.Counters {
			out.Counters[key] = value
		}
	}
	if snapshot.Gauges != nil {
		out.Gauges = make(map[string]float64, len(snapshot.Gauges))
		for key, value := range snapshot.Gauges {
			out.Gauges[key] = value
		}
	}
	if snapshot.Histograms != nil {
		out.Histograms = make(map[string]metrics.HistogramSummary, len(snapshot.Histograms))
		for key, value := range snapshot.Histograms {
			out.Histograms[key] = value
		}
	}
	out.Errors = append([]metrics.ErrorSample(nil), snapshot.Errors...)
	return out
}

func counterDelta(current, previous metrics.SnapshotData, names ...string) uint64 {
	var delta uint64
	for _, name := range names {
		now := current.Counters[name]
		before := previous.Counters[name]
		if now > before {
			delta += now - before
		}
	}
	return delta
}

func (r *Runner) waitReady(ctx context.Context) error {
	r.status.SetState(StateWaiting)
	deadlineCtx, cancel := context.WithTimeout(ctx, r.cfg.Retry.ReadinessTimeout)
	defer cancel()
	for {
		err := r.probe.CheckReady(deadlineCtx)
		if err == nil {
			r.status.SetLastError("")
			return nil
		}
		r.status.SetLastError(err.Error())
		if sleepErr := r.sleep(deadlineCtx, r.cfg.Retry.RestartBackoff); sleepErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("target readiness timeout: %w", err)
		}
	}
}

func (r *Runner) prepareConnect(ctx context.Context, assignment worker.Assignment) error {
	if starter, ok := r.workload.(worker.AssignmentStarter); ok {
		starter.BeginAssignment(assignment)
	}
	if err := r.workload.Prepare(ctx, assignment); err != nil {
		return err
	}
	if err := r.workload.Connect(ctx, assignment); err != nil {
		return err
	}
	if assignment.Scenario.Run.Warmup <= 0 {
		return nil
	}
	if err := r.workload.Warmup(ctx, assignment); err != nil {
		return err
	}
	r.captureCounterBaseline()
	return nil
}

func (r *Runner) stop(assignment worker.Assignment) {
	r.cooldown(assignment)
	r.status.SetState(StateStopped)
}

func (r *Runner) cooldown(assignment worker.Assignment) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	_ = r.workload.Cooldown(cleanupCtx, assignment)
}

func assignmentFromInputs(inputs BenchInputs, attempt int) (worker.Assignment, error) {
	workerPlan, ok := inputs.Plan.Workers[simulatorWorkerID]
	if !ok {
		return worker.Assignment{}, fmt.Errorf("missing simulator worker plan %q", simulatorWorkerID)
	}
	scenario := inputs.Scenario
	if attempt > 0 {
		prefix := strings.TrimSpace(scenario.Identity.ClientMsgPrefix)
		if prefix == "" {
			prefix = "sim-msg"
		}
		scenario.Identity.ClientMsgPrefix = fmt.Sprintf("%s-r%d", prefix, attempt)
	}
	return worker.Assignment{
		RunID:         inputs.Scenario.Run.ID,
		WorkerID:      simulatorWorkerID,
		ChannelOwners: inputs.Plan.ChannelOwners,
		Plan:          workerPlan,
		Target:        inputs.Target,
		Scenario:      scenario,
	}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NewRunID formats the default deterministic simulator run identifier.
func NewRunID(now time.Time) string {
	return "dev-sim-" + now.Format("20060102-150405")
}

// HTTPReadyProbe checks target HTTP health, readiness, and benchmark capabilities.
type HTTPReadyProbe struct {
	client *target.Client
}

// NewTargetProbe creates a target readiness checker from the derived bench target.
func NewTargetProbe(tgt model.Target) *HTTPReadyProbe {
	addrs := append([]string(nil), tgt.BenchAPI.Addrs...)
	if len(addrs) == 0 {
		addrs = append(addrs, tgt.API.Addrs...)
	}
	return &HTTPReadyProbe{client: target.NewClient(target.Config{APIAddrs: addrs, Token: tgt.BenchAPI.Token})}
}

// CheckReady verifies that the target can accept benchmark preparation traffic.
func (p *HTTPReadyProbe) CheckReady(ctx context.Context) error {
	if err := p.client.Healthz(ctx); err != nil {
		return err
	}
	if err := p.client.Readyz(ctx); err != nil {
		return err
	}
	capabilities, err := p.client.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !capabilities.Enabled {
		return fmt.Errorf("target bench api is disabled")
	}
	return nil
}
