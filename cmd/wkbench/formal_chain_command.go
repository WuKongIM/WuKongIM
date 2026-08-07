package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type formalChainCLIConfig struct {
	configPath string
	outputDir  string
	config     chatlifecycle.Config
}

func newFormalChainCommand(stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "formal-chain",
		Short: "Run the non-resumable formal Soak and aged capacity chain",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return commandExit{code: exitInternal, message: err.Error()}
			}
			return commandExit{code: exitConfig}
		},
	}
	var cli formalChainCLIConfig
	chat := &cobra.Command{
		Use:   "chat-lifecycle",
		Short: "Keep one coordinator process and worker generation through capacity recovery",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := loadFormalChainConfig(&cli); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runFormalChainCLI(cli, stderr))
		},
	}
	chat.Flags().StringVar(&cli.configPath, "config", "", "sealed formal chat-lifecycle YAML")
	chat.Flags().StringVar(&cli.outputDir, "output-dir", "", "formal-chain report root")
	command.AddCommand(chat)
	return command
}

func loadFormalChainConfig(cli *formalChainCLIConfig) error {
	if cli == nil || strings.TrimSpace(cli.configPath) == "" || strings.TrimSpace(cli.outputDir) == "" {
		return errors.New("--config and --output-dir are required")
	}
	cfg, err := chatlifecycle.LoadConfig(cli.configPath)
	if err != nil {
		return err
	}
	if cfg.Profile != chatlifecycle.ProfileFormal || cfg.Mode != chatlifecycle.ModeSoak ||
		cfg.Stage != chatlifecycle.StageFormal {
		return errors.New("formal-chain requires the strict formal Soak config")
	}
	cli.config, cli.outputDir = cfg, filepath.Clean(cli.outputDir)
	for _, path := range []string{
		filepath.Join(cli.outputDir, "formal", "final.json"),
		filepath.Join(cli.outputDir, "capacity", "final.json"),
		filepath.Join(cli.outputDir, "capacity.yaml"),
	} {
		if _, statErr := os.Stat(path); statErr == nil || !os.IsNotExist(statErr) {
			return errors.New("formal-chain output already contains terminal state")
		}
	}
	return nil
}

var newFormalChainCommandRunner = composeProductionFormalChainRunner

var runFormalChainCLI = func(cli formalChainCLIConfig, stderr io.Writer) int {
	runner, err := newFormalChainCommandRunner(cli)
	if err != nil || runner == nil {
		fmt.Fprintln(stderr, "formal-chain runner configuration failed")
		return exitInternal
	}
	return runPreparedChatLifecycleCLI(runner, stderr)
}

// productionFormalChainRunner owns one OS-process lifetime, one worker fence,
// one lifecycle proof loop, and one observation source through both reports.
type productionFormalChainRunner struct {
	cfg         chatlifecycle.Config
	outputDir   string
	runtime     *productionChatLifecycleRuntime
	controller  *chatlifecycle.ProductionEvidenceController
	coordinator *chatlifecycle.Coordinator
	stop        chan struct{}
	stopOnce    sync.Once
}

func composeProductionFormalChainRunner(cli formalChainCLIConfig) (chatLifecycleCommandRunner, error) {
	if cli.config.Validate() != nil || cli.config.Mode != chatlifecycle.ModeSoak || cli.outputDir == "" {
		return nil, errors.New("formal-chain production composition failed")
	}
	runtimeSafety, err := loadFormalRuntimeEnvelope(cli.config, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	formalOutput := filepath.Join(cli.outputDir, "formal")
	capacityOutput := filepath.Join(cli.outputDir, "capacity")
	if err := os.MkdirAll(formalOutput, 0o750); err != nil {
		return nil, errors.New("formal-chain production composition failed")
	}
	if err := os.MkdirAll(capacityOutput, 0o750); err != nil {
		return nil, errors.New("formal-chain production composition failed")
	}
	runtime, err := composeProductionChatLifecycleRuntime(cli.config, runtimeSafety)
	if err != nil {
		return nil, err
	}
	controller, err := runtime.controller(cli.config, formalOutput, true)
	if err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	formalCLI := chatLifecycleCLIConfig{config: cli.config, outputDir: formalOutput}
	coordinator, err := runtime.coordinator(formalCLI, controller, stop, true, nil)
	if err != nil {
		controller.Close()
		return nil, err
	}
	return &productionFormalChainRunner{
		cfg: cli.config, outputDir: cli.outputDir, runtime: runtime,
		controller: controller, coordinator: coordinator, stop: stop,
	}, nil
}

func validateFormalRuntimeEnvelope(cfg chatlifecycle.Config, now time.Time) error {
	_, err := loadFormalRuntimeEnvelope(cfg, now)
	return err
}

type formalBudgetLineItem struct {
	Kind       string `json:"kind"`
	Role       string `json:"role"`
	Quantity   int64  `json:"quantity"`
	CostMicros int64  `json:"cost_micros"`
}

type formalRuntimeEnvelope struct {
	createdAt, expiresAt time.Time
	operationalStop      int64
	committed            int64
	leaseHours           int64
	lineItems            []formalBudgetLineItem
}

func loadFormalRuntimeEnvelope(cfg chatlifecycle.Config, now time.Time) (*formalRuntimeEnvelope, error) {
	const hardLimit = int64(1_500_000_000)
	const operationalStop = int64(1_350_000_000)
	createdAt, createdErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(os.Getenv("WK_CHAT_LEASE_CREATED_AT")))
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(os.Getenv("WK_CHAT_LEASE_EXPIRES_AT")))
	if err != nil {
		return nil, errors.New("formal-chain lease expiry guard failed")
	}
	parse := func(name string) (int64, error) {
		raw := strings.TrimSpace(os.Getenv(name))
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || raw != strconv.FormatInt(value, 10) {
			return 0, errors.New("formal-chain budget guard failed")
		}
		return value, nil
	}
	limit, limitErr := parse("WK_CHAT_BUDGET_LIMIT_MICROS")
	stop, stopErr := parse("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS")
	committed, committedErr := parse("WK_CHAT_BUDGET_COMMITTED_MICROS")
	estimated, estimatedErr := parse("WK_CHAT_BUDGET_ESTIMATED_MICROS")
	encodedItems, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("WK_CHAT_BUDGET_LINE_ITEMS_BASE64")))
	var lineItems []formalBudgetLineItem
	if decodeErr == nil {
		decodeErr = json.Unmarshal(encodedItems, &lineItems)
	}
	var minimumRemaining time.Duration
	switch cfg.Stage {
	case chatlifecycle.StageFormal:
		minimumRemaining = cfg.Thresholds.Timeline.Final + cfg.Capacity.MaximumDuration + cfg.Capacity.RecoveryDuration + time.Hour
	case chatlifecycle.StageRehearsal:
		minimumRemaining = 2*time.Hour + time.Hour
	default:
		return nil, errors.New("formal-chain immutable budget or expiry guard failed")
	}
	leaseHours := int64(math.Ceil(expiresAt.Sub(createdAt).Hours()))
	var quoted int64
	itemsValid := len(lineItems) > 0 && leaseHours > 0
	for _, item := range lineItems {
		if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Role) == "" || item.Quantity <= 0 ||
			item.CostMicros <= 0 || item.CostMicros > math.MaxInt64-quoted {
			itemsValid = false
			break
		}
		quoted += item.CostMicros
		switch item.Kind {
		case "postpaid_host_hour":
			itemsValid = itemsValid && item.Quantity%leaseHours == 0
		case "eip_public_egress_gib", "eip_retention_policy_risk_hour":
		default:
			itemsValid = false
		}
	}
	if createdErr != nil || limitErr != nil || stopErr != nil || committedErr != nil || estimatedErr != nil || decodeErr != nil ||
		limit != hardLimit || stop != operationalStop || committed < 0 || estimated <= 0 ||
		committed >= stop || estimated > stop-committed || quoted != estimated || !itemsValid ||
		createdAt.After(now.UTC()) || !createdAt.Before(expiresAt) || expiresAt.Before(now.UTC().Add(minimumRemaining)) {
		return nil, errors.New("formal-chain immutable budget or expiry guard failed")
	}
	return &formalRuntimeEnvelope{
		createdAt: createdAt.UTC(), expiresAt: expiresAt.UTC(), operationalStop: stop,
		committed: committed, leaseHours: leaseHours, lineItems: append([]formalBudgetLineItem(nil), lineItems...),
	}, nil
}

func (g *formalRuntimeEnvelope) Observe(
	ctx context.Context,
	at time.Time,
	networkTransmitBytes uint64,
) (chatlifecycle.RuntimeSafetySnapshot, error) {
	if g == nil || ctx == nil || at.IsZero() {
		return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
	}
	if err := ctx.Err(); err != nil {
		return chatlifecycle.RuntimeSafetySnapshot{}, err
	}
	at = at.UTC()
	if at.Before(g.createdAt) {
		return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
	}
	heldHours := int64(math.Ceil(at.Sub(g.createdAt).Hours()))
	if heldHours < 1 {
		heldHours = 1
	}
	if heldHours > g.leaseHours {
		heldHours = g.leaseHours
	}
	accrued := g.committed
	trafficGiB := int64(0)
	if networkTransmitBytes > 0 {
		const bytesPerGiB = uint64(1 << 30)
		if networkTransmitBytes > math.MaxUint64-(bytesPerGiB-1) {
			return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
		}
		trafficGiB = int64((networkTransmitBytes + bytesPerGiB - 1) / bytesPerGiB)
		if trafficGiB < 0 {
			return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
		}
	}
	for _, item := range g.lineItems {
		var quantity int64
		switch item.Kind {
		case "postpaid_host_hour":
			quantity = (item.Quantity / g.leaseHours) * heldHours
		case "eip_public_egress_gib":
			quantity = trafficGiB
		case "eip_retention_policy_risk_hour":
			quantity = item.Quantity
		default:
			return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
		}
		cost, ok := ceilingCostFraction(item.CostMicros, quantity, item.Quantity)
		if !ok || cost > math.MaxInt64-accrued {
			return chatlifecycle.RuntimeSafetySnapshot{}, errors.New("formal-chain runtime guard failed")
		}
		accrued += cost
	}
	remaining := g.expiresAt.Sub(at)
	cause := chatlifecycle.RuntimeSafetyOK
	if remaining <= time.Hour {
		cause = chatlifecycle.RuntimeSafetyLeaseExpiryRisk
	}
	if accrued >= g.operationalStop {
		cause = chatlifecycle.RuntimeSafetyBudgetStop
	}
	return chatlifecycle.RuntimeSafetySnapshot{
		Cause: cause, AccruedCostMicros: accrued,
		NetworkTransmitBytes: networkTransmitBytes, LeaseRemaining: remaining,
	}, nil
}

func ceilingCostFraction(cost, quantity, quotedQuantity int64) (int64, bool) {
	if cost <= 0 || quantity < 0 || quotedQuantity <= 0 || quantity > math.MaxInt64/cost {
		return 0, false
	}
	product := cost * quantity
	if product > math.MaxInt64-(quotedQuantity-1) {
		return 0, false
	}
	return (product + quotedQuantity - 1) / quotedQuantity, true
}

func (r *productionFormalChainRunner) Run(ctx context.Context) (chatLifecycleRunResult, error) {
	if r == nil || ctx == nil || r.runtime == nil || r.controller == nil || r.coordinator == nil {
		return chatLifecycleRunResult{}, errors.New("formal-chain production runner failed")
	}
	defer r.controller.Close()
	formalOutput := filepath.Join(r.outputDir, "formal")
	formalPath := filepath.Join(formalOutput, "final.json")
	formalResult := r.coordinator.Run(ctx, r.cfg)
	formalReport, err := chatlifecycle.ReadReport(formalPath)
	if err != nil {
		return coordinatorRunResult(formalResult, formalPath), errors.New("formal-chain formal report unavailable")
	}
	formalSummary := fmt.Sprintf(
		"chat-lifecycle outcome=%s cause=%s report=%s\n",
		formalReport.Verdict.Outcome, formalReport.Verdict.Cause, formalPath,
	)
	if formalReport.Verdict.Outcome != chatlifecycle.VerdictPass {
		return reportRunResult(formalReport, formalSummary), nil
	}

	// From this point until capacity finishes, the workers are live. Any local
	// preparation failure performs a direct bounded stop; it never leaves a
	// generation running for a later process to resume.
	defer r.stopLiveWorkers(formalResult.Fence)
	if !formalReport.Continuous || formalResult.Grant.Sequence == 0 || formalResult.Continuation == nil ||
		formalResult.Fence.Generation != 1 {
		return internalFormalChainFailure(formalSummary), errors.New("formal-chain continuity evidence invalid")
	}
	defer formalResult.Continuation.CancelObservation()
	capacityPath := filepath.Join(r.outputDir, "capacity.yaml")
	capacityCfg, err := chatlifecycle.PrepareCapacityConfig(r.cfg, formalReport, formalPath)
	if err != nil {
		return internalFormalChainFailure(formalSummary), err
	}
	if err := writeFormalChainConfig(capacityPath, capacityCfg); err != nil {
		return internalFormalChainFailure(formalSummary), err
	}
	capacityOutput := filepath.Join(r.outputDir, "capacity")
	if err := r.controller.ContinueCapacity(capacityCfg, capacityOutput); err != nil {
		return internalFormalChainFailure(formalSummary), err
	}
	capacityCLI := chatLifecycleCLIConfig{
		configPath: capacityPath, checkpointPath: formalPath, outputDir: capacityOutput,
		config: capacityCfg, checkpoint: formalReport,
	}
	capacityCoordinator, err := r.runtime.coordinator(capacityCLI, r.controller, r.stop, false, formalResult.Continuation)
	if err != nil {
		return internalFormalChainFailure(formalSummary), err
	}
	capacityResult := capacityCoordinator.Run(ctx, capacityCfg)
	capacityReportPath := filepath.Join(capacityOutput, "final.json")
	capacityReport, err := chatlifecycle.ReadReport(capacityReportPath)
	if err != nil {
		result := coordinatorRunResult(capacityResult, capacityReportPath)
		result.Summary = formalSummary + result.Summary
		return result, errors.New("formal-chain capacity report unavailable")
	}
	summary := formalSummary + fmt.Sprintf(
		"chat-lifecycle outcome=%s cause=%s report=%s\n",
		capacityReport.Verdict.Outcome, capacityReport.Verdict.Cause, capacityReportPath,
	)
	return reportRunResult(capacityReport, summary), nil
}

func (r *productionFormalChainRunner) RequestStop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stop) })
}

func (r *productionFormalChainRunner) stopLiveWorkers(fence chatlifecycle.WorkerFence) {
	if r == nil || r.runtime == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var joined sync.WaitGroup
	for _, worker := range r.runtime.workers {
		joined.Add(1)
		go func(worker chatlifecycle.CoordinatorWorker) {
			defer joined.Done()
			_, _ = worker.Stop(ctx, chatlifecycle.WorkerStopRequest{WorkerFence: fence})
		}(worker)
	}
	joined.Wait()
}

func writeFormalChainConfig(path string, cfg chatlifecycle.Config) error {
	body, err := yaml.Marshal(cfg)
	if err != nil || len(body) == 0 {
		return errors.New("formal-chain capacity config failed")
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".capacity-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
		return errors.New("formal-chain capacity config already exists")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func reportRunResult(report chatlifecycle.Report, summary string) chatLifecycleRunResult {
	return chatLifecycleRunResult{Verdict: chatlifecycle.VerdictSnapshot{
		Outcome: report.Verdict.Outcome, Cause: report.Verdict.Cause, Terminal: report.Verdict.Terminal,
		CleanupErrorCount: report.Verdict.CleanupErrorCount,
		CleanupErrors:     append([]chatlifecycle.VerdictCleanupErrorCode(nil), report.Verdict.CleanupErrors...),
	}, Summary: summary}
}

func coordinatorRunResult(result chatlifecycle.CoordinatorResult, reportPath string) chatLifecycleRunResult {
	verdict := chatLifecycleCoordinatorVerdict(result)
	return chatLifecycleRunResult{
		Verdict: verdict,
		Summary: fmt.Sprintf(
			"chat-lifecycle outcome=%s cause=%s coordinator_code=%s preflight_code=%s report=%s\n",
			verdict.Outcome, verdict.Cause, result.Code, result.Preflight.Code, reportPath,
		),
	}
}

func internalFormalChainFailure(summary string) chatLifecycleRunResult {
	return chatLifecycleRunResult{Verdict: chatlifecycle.VerdictSnapshot{
		Outcome: chatlifecycle.VerdictHarnessInvalid, Cause: chatlifecycle.VerdictCauseInvalidObservation, Terminal: true,
	}, Summary: summary + "formal-chain outcome=harness_invalid cause=invalid_continuation\n"}
}
