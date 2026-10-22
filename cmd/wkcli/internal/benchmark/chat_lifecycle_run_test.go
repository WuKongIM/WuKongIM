package benchmark

import (
	"bytes"
	"context"
	"os"
	"strings"
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

func TestChatLifecycleFinalizationSummaryRetainsClosedBoundaryWithoutChangingVerdict(t *testing.T) {
	result := chatlifecycle.CoordinatorResult{
		Outcome: chatlifecycle.CoordinatorHarnessInvalid, Code: chatlifecycle.CoordinatorCodeFinalize,
		FinalizationFailure: chatlifecycle.FinalizationFailure{
			Stage: "meta_checkpoint", Reason: "invalid", ObservedDecision: chatlifecycle.CoordinatorProductFailure,
			ObservedOutcome: chatlifecycle.VerdictProductFailure, ObservedCause: chatlifecycle.VerdictCauseMessageLoss, ObservedTerminal: true,
		},
	}
	verdict := chatLifecycleCoordinatorVerdict(result)
	if verdict.Outcome != chatlifecycle.VerdictHarnessInvalid || verdict.Cause != chatlifecycle.VerdictCauseInvalidObservation || chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleHarness {
		t.Fatalf("diagnostic observation replaced the failed finalization verdict: %+v", verdict)
	}
	got := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	for _, fragment := range []string{"finalization_stage=meta_checkpoint", "finalization_reason=invalid", "observed_decision=product_failure", "observed_outcome=product_failure", "observed_cause=message_loss", "observed_terminal=true", "report=unavailable"} {
		if !strings.Contains(got, fragment) {
			t.Errorf("summary %q misses %q", got, fragment)
		}
	}
	// A report selected by the existing reader keeps its own verdict; the
	// coordinator failure remains an additional diagnostic on that path.
	chosenReportVerdict := chatlifecycle.VerdictSnapshot{Outcome: chatlifecycle.VerdictProductFailure, Cause: chatlifecycle.VerdictCauseTerminalSend, Terminal: true}
	reported := chatLifecycleCoordinatorSummary(chosenReportVerdict, result, "existing-final.json")
	if !strings.HasPrefix(reported, "chat-lifecycle outcome=product_failure cause=terminal_send ") || !strings.Contains(reported, "finalization_stage=meta_checkpoint") || chatLifecycleVerdictExitCode(chosenReportVerdict) != exitChatLifecycleProduct {
		t.Fatalf("finalization diagnostics changed the selected report projection: %q", reported)
	}
	result.FinalizationFailure = chatlifecycle.FinalizationFailure{
		Stage: "private-token-stage", Reason: "private-token-reason", ObservedOutcome: "private-token-outcome", ObservedCause: "private-token-cause", ObservedTerminal: true,
	}
	got = chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	if strings.Contains(got, "private-token") || !strings.Contains(got, "finalization_stage=controller_unknown") || !strings.Contains(got, "observed_terminal=false") {
		t.Fatalf("summary exposed unbounded finalization values: %q", got)
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
