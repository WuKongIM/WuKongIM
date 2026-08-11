package main

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestChatLifecycleExitCodesPreserveSuccessAndFailureClasses(t *testing.T) {
	tests := []struct {
		outcome chatlifecycle.VerdictOutcome
		want    int
	}{
		{chatlifecycle.VerdictPass, 0},
		{chatlifecycle.VerdictPassedWithCapacityWarning, 0},
		{chatlifecycle.VerdictProductFailure, exitChatLifecycleProduct},
		{chatlifecycle.VerdictHarnessInvalid, exitChatLifecycleHarness},
		{chatlifecycle.VerdictInsufficientEvidence, exitChatLifecycleHarness},
		{chatlifecycle.VerdictInfrastructureFailure, exitChatLifecycleInfrastructure},
		{chatlifecycle.VerdictOperatorStop, exitChatLifecycleOperatorStop},
	}
	for _, test := range tests {
		code := chatLifecycleVerdictExitCode(chatlifecycle.VerdictSnapshot{Terminal: true, Outcome: test.outcome})
		if code != test.want {
			t.Fatalf("outcome %s exit = %d, want %d", test.outcome, code, test.want)
		}
	}
	primary := []int{0, exitChatLifecycleProduct, exitChatLifecycleHarness, exitChatLifecycleInfrastructure, exitChatLifecycleOperatorStop}
	for index, code := range primary {
		for previous := 0; previous < index; previous++ {
			if code == primary[previous] {
				t.Fatalf("primary chat-lifecycle exit code %d is duplicated", code)
			}
		}
	}
	if code := chatLifecycleVerdictExitCode(chatlifecycle.VerdictSnapshot{}); code != exitInternal {
		t.Fatalf("nonterminal verdict exit = %d, want %d", code, exitInternal)
	}
}

func TestChatLifecycleUnavailableSummaryIncludesObserverCode(t *testing.T) {
	result := chatlifecycle.CoordinatorResult{
		Outcome:      chatlifecycle.CoordinatorProductFailure,
		Code:         chatlifecycle.CoordinatorCodeObserver,
		GrantFailure: chatlifecycle.CoordinatorGrantFailureDelivery,
		WorkerFailure: chatlifecycle.CoordinatorWorkerFailure{
			WorkerID: 2, RuntimeCode: chatlifecycle.RuntimeFailureEngineCPUSaturated,
		},
		ObserverCode: chatlifecycle.ObserverCodeClusterHealth,
	}
	verdict := chatLifecycleCoordinatorVerdict(result)
	got := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	const want = "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer grant_failure_code=delivery worker_runtime_code=engine_cpu_saturated observer_code=cluster_health preflight_code= report=unavailable\n"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestChatLifecycleFirstSignalRequestsGracefulStopAndWaitsForFinalResult(t *testing.T) {
	runner := newSignalTestChatLifecycleRunner()
	signals := make(chan os.Signal, 2)
	done := make(chan int, 1)
	go func() {
		done <- runChatLifecycleRunner(context.Background(), runner, signals, func(int) {}, &bytes.Buffer{})
	}()
	<-runner.started
	signals <- os.Interrupt
	<-runner.stopRequested
	select {
	case code := <-done:
		t.Fatalf("first signal returned before final result with code %d", code)
	default:
	}
	close(runner.release)
	select {
	case code := <-done:
		if code != exitChatLifecycleOperatorStop {
			t.Fatalf("graceful signal exit = %d, want %d", code, exitChatLifecycleOperatorStop)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful signal did not wait for final result")
	}
}

func TestChatLifecycleSecondSignalForcesPromptExit(t *testing.T) {
	runner := newSignalTestChatLifecycleRunner()
	signals := make(chan os.Signal, 2)
	hardExit := make(chan int, 1)
	done := make(chan int, 1)
	go func() {
		done <- runChatLifecycleRunner(context.Background(), runner, signals, func(code int) {
			hardExit <- code
		}, &bytes.Buffer{})
	}()
	<-runner.started
	signals <- os.Interrupt
	<-runner.stopRequested
	signals <- syscall.SIGTERM
	select {
	case code := <-hardExit:
		if code != 143 {
			t.Fatalf("hard exit callback = %d, want 143", code)
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not force prompt exit")
	}
	select {
	case code := <-done:
		if code != 143 {
			t.Fatalf("second signal return = %d, want 143", code)
		}
	case <-time.After(time.Second):
		t.Fatal("second signal handler did not return")
	}
	close(runner.release)
}

func TestNewChatLifecycleCommandRunnerComposesProductionRunner(t *testing.T) {
	t.Setenv("WK_BENCH_API_TOKEN", "bench-test-token")
	t.Setenv("WK_BENCH_WORKER_TOKEN", "worker-test-token")
	t.Setenv("WK_CHAT_LIFECYCLE_BENCH_TOKEN_FILE", "")
	t.Setenv("WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE", "")
	cfg := chatlifecycle.LocalConfig()
	cfg.RunID = "cli-production-composition"
	runner, err := newChatLifecycleCommandRunner(chatLifecycleCLIConfig{
		config: cfg, outputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runner.(*productionChatLifecycleRunner); !ok {
		t.Fatalf("runner type = %T, want productionChatLifecycleRunner", runner)
	}
}

type signalTestChatLifecycleRunner struct {
	started       chan struct{}
	stopRequested chan struct{}
	release       chan struct{}
}

func newSignalTestChatLifecycleRunner() *signalTestChatLifecycleRunner {
	return &signalTestChatLifecycleRunner{
		started: make(chan struct{}), stopRequested: make(chan struct{}), release: make(chan struct{}),
	}
}

func (r *signalTestChatLifecycleRunner) Run(context.Context) (chatLifecycleRunResult, error) {
	close(r.started)
	<-r.release
	return chatLifecycleRunResult{Verdict: chatlifecycle.VerdictSnapshot{
		Terminal: true, Outcome: chatlifecycle.VerdictOperatorStop, Cause: chatlifecycle.VerdictCauseOperatorRequested,
	}}, nil
}

func (r *signalTestChatLifecycleRunner) RequestStop() { close(r.stopRequested) }
