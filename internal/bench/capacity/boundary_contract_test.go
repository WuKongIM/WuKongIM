package capacity

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestConfigValidateRejectsUnsafeSearchBounds(t *testing.T) {
	t.Parallel()

	valid := DefaultConfig()
	valid.APIAddrs = []string{" http://127.0.0.1:15001 "}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "zero start", mutate: func(c *Config) { c.StartQPS = 0 }, want: "start-qps"},
		{name: "nan start", mutate: func(c *Config) { c.StartQPS = math.NaN() }, want: "start-qps"},
		{name: "zero max", mutate: func(c *Config) { c.MaxQPS = 0 }, want: "max-qps"},
		{name: "descending range", mutate: func(c *Config) { c.MaxQPS = c.StartQPS - 1 }, want: "greater than or equal"},
		{name: "step one", mutate: func(c *Config) { c.StepFactor = 1 }, want: "step-factor"},
		{name: "zero duration", mutate: func(c *Config) { c.Duration = 0 }, want: "duration"},
		{name: "zero warmup", mutate: func(c *Config) { c.Warmup = 0 }, want: "warmup"},
		{name: "negative cooldown", mutate: func(c *Config) { c.Cooldown = -1 }, want: "cooldown"},
		{name: "zero p99", mutate: func(c *Config) { c.StableP99 = 0 }, want: "stable-p99"},
		{name: "actual ratio above one", mutate: func(c *Config) { c.MinActualRatio = 1.01 }, want: "min-actual-ratio"},
		{name: "negative send error", mutate: func(c *Config) { c.MaxSendackErrorRate = -1 }, want: "max-sendack-error-rate"},
		{name: "infinite connect error", mutate: func(c *Config) { c.MaxConnectErrorRate = math.Inf(1) }, want: "max-connect-error-rate"},
		{name: "zero binary delta", mutate: func(c *Config) { c.BinarySearchMinDeltaRatio = 0 }, want: "binary-search-min-delta-ratio"},
		{name: "zero group members", mutate: func(c *Config) { c.GroupMembers = 0 }, want: "group-members"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestSearchHonorsValidationRunnerErrorsAndMaximumBoundary(t *testing.T) {
	t.Parallel()

	invalid := DefaultConfig()
	if _, err := Search(context.Background(), invalid, nil); err == nil {
		t.Fatal("Search(invalid) error = nil")
	}
	valid := DefaultConfig()
	valid.APIAddrs = []string{"http://127.0.0.1:15001"}
	if _, err := Search(context.Background(), valid, nil); err == nil || !strings.Contains(err.Error(), "attempt runner") {
		t.Fatalf("Search(nil runner) error = %v", err)
	}
	wantErr := errors.New("attempt unavailable")
	if _, err := Search(context.Background(), valid, attemptRunnerFunc(func(context.Context, Attempt) (AttemptResult, error) {
		return AttemptResult{}, wantErr
	})); !errors.Is(err, wantErr) {
		t.Fatalf("Search(runner error) = %v, want %v", err, wantErr)
	}

	valid.StartQPS = 100
	valid.MaxQPS = 250
	valid.StepFactor = 2
	valid.BinarySearch = false
	var offered []float64
	result, err := Search(context.Background(), valid, attemptRunnerFunc(func(_ context.Context, attempt Attempt) (AttemptResult, error) {
		offered = append(offered, attempt.OfferedQPS)
		return AttemptResult{Attempt: attempt, Passed: true}, nil
	}))
	if err != nil {
		t.Fatalf("Search(max boundary): %v", err)
	}
	if want := []float64{100, 200, 250}; !reflect.DeepEqual(offered, want) {
		t.Fatalf("offered QPS = %v, want %v", offered, want)
	}
	if result.MaxStableQPS != 250 || result.Status != StatusPassed {
		t.Fatalf("result = %+v", result)
	}
}

func TestCapacityAccountingHelpersRemainSaturatingAndBounded(t *testing.T) {
	t.Parallel()

	if activateChannelsRatePerChannel(0) != 0 || positiveUint64(-1) != 0 {
		t.Fatal("non-positive activation inputs did not saturate at zero")
	}
	for _, test := range []struct {
		channels uint64
		success  uint64
		errors   uint64
		want     uint64
	}{
		{channels: 10, success: 10, want: 0},
		{channels: 10, success: 7, errors: 3, want: 0},
		{channels: 10, success: 7, errors: 1, want: 2},
	} {
		if got := activationBacklog(test.channels, test.success, test.errors); got != test.want {
			t.Fatalf("activationBacklog(%d,%d,%d) = %d, want %d", test.channels, test.success, test.errors, got, test.want)
		}
	}
	if scheduledMessagesForAttempt(Attempt{OfferedQPS: math.NaN()}, time.Second) != 0 ||
		scheduledMessagesForAttempt(Attempt{OfferedQPS: 1}, 0) != 0 ||
		scheduledMessagesForAttempt(Attempt{OfferedQPS: 0.1}, time.Second) != 0 {
		t.Fatal("invalid or sub-message schedules did not saturate at zero")
	}
	if got := backlogMessages(10, 7, 5); got != 0 {
		t.Fatalf("backlogMessages() = %d, want 0", got)
	}
	if totalUsersForScenario(0, 0, 0) != 1 || concurrencyForQPS(0) != 0 || concurrencyForQPS(1) != 1 {
		t.Fatal("scenario minimum bounds changed")
	}
	if attemptReportDir(" ", Attempt{OfferedQPS: 1}) != "" || timestampedReportDir("", time.Now()) != "" {
		t.Fatal("empty report roots produced filesystem paths")
	}
}

func TestActivationBatchAndFailureHelpersPreserveExactEvidence(t *testing.T) {
	t.Parallel()

	if ranges := activateChannelsRuntimeRanges(0, 10); ranges != nil {
		t.Fatalf("zero channel ranges = %#v", ranges)
	}
	wantRanges := []model.ChannelRuntimeRange{{Start: 0, End: 2}, {Start: 2, End: 4}, {Start: 4, End: 5}}
	if got := activateChannelsRuntimeRanges(5, 2); !reflect.DeepEqual(got, wantRanges) {
		t.Fatalf("ranges = %#v, want %#v", got, wantRanges)
	}
	if holdSampleCount(0, time.Second) != 0 || holdSampleCount(time.Second, 0) != 1 || holdSampleCount(5*time.Second, 2*time.Second) != 3 {
		t.Fatal("hold sample count violated bounded ceiling")
	}
	if nodes := activeNodeRuntimeDistribution(nil); nodes != nil {
		t.Fatalf("empty distribution = %#v", nodes)
	}
	evaluation := ActivateChannelsEvaluation{Passed: true}
	addActivateChannelsFailure(&evaluation, "probe_failed")
	addActivateChannelsFailure(&evaluation, "probe_failed")
	addActivateChannelsFailure(nil, "ignored")
	addActivateChannelsFailure(&evaluation, "")
	if evaluation.Passed || !reflect.DeepEqual(evaluation.FailureReasons, []string{"probe_failed"}) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestCapacityResultAndRunnerColdBoundaries(t *testing.T) {
	t.Parallel()

	if (Result{Status: StatusPassed}).ExitCode() != ExitSuccess || (Result{Status: StatusFailed}).ExitCode() != ExitNoStableAttempt {
		t.Fatal("capacity exit-code mapping changed")
	}
	if err := WriteResult(" ", Result{}); err != nil {
		t.Fatalf("WriteResult(empty): %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := WriteResult(filepath.Join(filePath, "child"), Result{}); err == nil {
		t.Fatal("WriteResult(path below file) error = nil")
	}
	var nilRunner *ActivateChannelsRunner
	if result, err := nilRunner.Run(context.Background()); err == nil || result.Status != StatusFailed {
		t.Fatalf("nil ActivateChannelsRunner.Run() = (%+v, %v)", result, err)
	}
	runner := &ActivateChannelsRunner{cfg: DefaultActivateChannelsConfig()}
	runner.setDefaults()
	if runner.base == nil || runner.target == nil || runner.run == nil || runner.now == nil {
		t.Fatalf("setDefaults() left incomplete runner: %+v", runner)
	}
	runner.base.workers = []model.Worker{{ID: "existing", Addr: "memory://worker"}}
	started, err := runner.ensureWorker()
	if err != nil || started {
		t.Fatalf("ensureWorker(existing) = (%v, %v)", started, err)
	}
}

func TestCoordinatorFailureClassificationKeepsStableCapacityReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     coordinator.RunStatus
		wantReason string
		wantWorker int
	}{
		{name: "worker", status: coordinator.StatusWorkerFailed, wantReason: "worker_failed", wantWorker: 1},
		{name: "target", status: coordinator.StatusTargetUnavailable, wantReason: "target_unavailable"},
		{name: "canceled", status: coordinator.StatusCanceled, wantReason: string(coordinator.StatusCanceled)},
		{name: "internal", status: coordinator.StatusInternalFailed, wantReason: string(coordinator.StatusInternalFailed)},
		{name: "config", status: coordinator.StatusConfigFailed, wantReason: string(coordinator.StatusConfigFailed)},
		{name: "preflight", status: coordinator.StatusPreflightFailed, wantReason: string(coordinator.StatusPreflightFailed)},
		{name: "hard limit", status: coordinator.StatusHardLimitFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt := AttemptResult{Passed: true}
			applyCoordinatorFailure(&attempt, coordinator.RunResult{Status: test.status})
			if attempt.Passed || attempt.FailureReason != test.wantReason || attempt.WorkerFailed != test.wantWorker {
				t.Fatalf("classified attempt = %+v", attempt)
			}
		})
	}
	applyCoordinatorFailure(nil, coordinator.RunResult{Status: coordinator.StatusWorkerFailed})
}

func TestAttemptPassStatusUsesDeclaredFailurePriority(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	attempt := Attempt{OfferedQPS: 100}
	passing := AttemptResult{ActualQPS: 100, SendackP99: cfg.StableP99}
	tests := []struct {
		name   string
		result AttemptResult
		want   string
	}{
		{name: "worker", result: AttemptResult{WorkerFailed: 1}, want: "worker_failed"},
		{name: "connect", result: AttemptResult{ConnectErrorRate: 0.1}, want: "connect_error_rate_exceeded"},
		{name: "sendack", result: AttemptResult{SendackErrorRate: 0.1}, want: "sendack_error_rate_exceeded"},
		{name: "actual qps", result: AttemptResult{ActualQPS: 1}, want: "actual_qps_below_min_ratio"},
		{name: "p99", result: AttemptResult{ActualQPS: 100, SendackP99: cfg.StableP99 + 1}, want: "sendack_p99_exceeded"},
	}
	for _, test := range tests {
		if passed, reason := attemptPassStatus(cfg, attempt, test.result); passed || reason != test.want {
			t.Fatalf("%s classification = (%v, %q), want (false, %q)", test.name, passed, reason, test.want)
		}
	}
	if passed, reason := attemptPassStatus(cfg, attempt, passing); !passed || reason != "" {
		t.Fatalf("passing classification = (%v, %q)", passed, reason)
	}
}

func TestHotChannelDefaultsPreserveSingleChannelFanIn(t *testing.T) {
	t.Parallel()

	cfg := DefaultHotChannelConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Profile = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate(default profile): %v", err)
	}
	cfg.Senders = 0
	scenario := BuildHotChannelScenario(cfg, Attempt{OfferedQPS: 0})
	if scenario.Online.TotalUsers != 1 || scenario.Channels.Profiles[0].Members.Count != 1 || scenario.Messages.Traffic[0].Concurrency != 1 {
		t.Fatalf("minimum hot-channel scenario = %+v", scenario)
	}
	if got := hotChannelConcurrencyForQPS(1000, 2); got != 100 {
		t.Fatalf("hot concurrency = %d, want 100", got)
	}
	runner := NewHotChannelRunner(DefaultHotChannelConfig(), DiscoveredTarget{})
	if runner == nil || runner.base == nil {
		t.Fatal("NewHotChannelRunner() returned incomplete runner")
	}
}
