package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func chatLifecycleGeneration(mode chatlifecycle.Mode) uint64 {
	if mode == chatlifecycle.ModeCapacity {
		return 2
	}
	return 1
}

// productionChatLifecycleRunner is the command composition root. Scheduling,
// lifecycle, capacity, and verdict policy remain in chatlifecycle.
type productionChatLifecycleRunner struct {
	cfg         chatlifecycle.Config
	outputDir   string
	coordinator *chatlifecycle.Coordinator
	controller  *chatlifecycle.ProductionEvidenceController
	stop        chan struct{}
	stopOnce    sync.Once
}

type productionChatLifecycleRuntime struct {
	// assignment is the immutable 12-group placement shared by both phases.
	assignment chatlifecycle.LifecycleSlotAssignment
	// workers are the three live control clients whose exact fence survives the phase boundary.
	workers []chatlifecycle.CoordinatorWorker
	// capacityWorkers switch the existing worker generation to scheduled capacity rates.
	capacityWorkers [3]chatlifecycle.ProductionCapacityWorker
	// setup creates formal group metadata only before the initial Soak assignment.
	setup chatlifecycle.CoordinatorGroupSetup
	// observation owns one uninterrupted host, service, cluster, and disk evidence stream.
	observation *chatlifecycle.ProductionObservationSource
	// observer samples the unchanged service cluster throughout both phases.
	observer chatlifecycle.CoordinatorObserver
	// lifecycle proves cold unload and reheat without restarting its proof loop.
	lifecycle *chatlifecycle.ProductionLifecycle
	// accounting is the monotonic metadata ledger shared by both report reducers.
	accounting *chatlifecycle.MetaCreateAccounting
	// meta enforces the same durable metadata model throughout the process lifetime.
	meta *chatlifecycle.ProductionMetaController
	// dataset probes the unchanged target dataset before aged-capacity admission.
	dataset *chatlifecycle.CapacityDatasetProbe
	// preflight validates the sealed topology and host resources at each phase boundary.
	preflight chatlifecycle.CoordinatorPreflight
}

func composeProductionChatLifecycleRuntime(
	cfg chatlifecycle.Config,
	runtimeSafety chatlifecycle.RuntimeSafetyGuard,
) (*productionChatLifecycleRuntime, error) {
	if cfg.Validate() != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	credentials, err := chatlifecycle.LoadCredentials()
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	assignment, err := chatlifecycle.NewInitialLifecycleSlotAssignment()
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}

	workerInterfaces := make([]chatlifecycle.CoordinatorWorker, len(cfg.Observation.Workers))
	var lifecycleWorkers [3]chatlifecycle.ProductionLifecycleWorker
	var capacityWorkers [3]chatlifecycle.ProductionCapacityWorker
	workerTokens := make(map[string]string, len(cfg.Observation.Workers))
	for index, endpoint := range cfg.Observation.Workers {
		worker, workerErr := chatlifecycle.NewWorkerClient(chatlifecycle.WorkerClientConfig{
			BaseURL: endpoint.Address, ControlToken: credentials.WorkerToken(), HTTPClient: httpClient,
		})
		if workerErr != nil {
			return nil, errors.New("chat-lifecycle production composition failed")
		}
		workerInterfaces[index] = worker
		lifecycleWorkers[index] = worker
		capacityWorkers[index] = worker
		workerTokens[endpoint.Name] = credentials.WorkerToken()
	}

	targetClient := target.NewClient(target.Config{
		APIAddrs: append([]string(nil), cfg.Observation.APIAddrs...),
		Token:    credentials.BenchToken(), HTTPClient: httpClient,
	})
	setup, err := chatlifecycle.NewGroupSetup(chatlifecycle.GroupSetupOptions{
		Target: targetClient, MaxChannelsPerBatch: 2_000, MaxSubscribersPerBatch: 4_096,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	observation, err := chatlifecycle.NewProductionObservationSource(chatlifecycle.ProductionObservationOptions{
		Config: cfg, BenchToken: credentials.BenchToken(), HTTPClient: httpClient, RuntimeSafety: runtimeSafety,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	observer := chatlifecycle.NewObserver(chatlifecycle.ObserverOptions{
		BenchToken: credentials.BenchToken(), HTTPClient: httpClient, SampleSink: observation,
	})
	lifecycle, err := chatlifecycle.NewProductionLifecycle(chatlifecycle.ProductionLifecycleOptions{
		Workers: lifecycleWorkers, Prober: targetClient, SlotAssignment: assignment,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	accounting := chatlifecycle.NewMetaCreateAccounting()
	var metrics [3]chatlifecycle.ProductionMetaMetricsSource
	for index, endpoint := range cfg.Observation.ServiceNodes {
		metrics[index] = target.NewClient(target.Config{
			APIAddrs: []string{endpoint.Address}, Token: credentials.BenchToken(), HTTPClient: httpClient,
		})
	}
	meta, err := chatlifecycle.NewProductionMetaController(chatlifecycle.ProductionMetaControllerOptions{
		Config: cfg, Metrics: metrics, Accounting: accounting,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	dataset := chatlifecycle.NewCapacityDatasetProbe(chatlifecycle.CapacityDatasetProbeOptions{
		BenchToken: credentials.BenchToken(), HTTPClient: httpClient,
	})
	preflight := chatlifecycle.NewPreflight(chatlifecycle.PreflightOptions{
		BenchToken: credentials.BenchToken(), WorkerTokens: workerTokens, HTTPClient: httpClient,
	})
	return &productionChatLifecycleRuntime{
		assignment: assignment, workers: workerInterfaces,
		capacityWorkers: capacityWorkers, setup: setup, observation: observation, observer: observer,
		lifecycle: lifecycle, accounting: accounting, meta: meta, dataset: dataset, preflight: preflight,
	}, nil
}

func (r *productionChatLifecycleRuntime) controller(
	cfg chatlifecycle.Config,
	outputDir string,
	continuous bool,
) (*chatlifecycle.ProductionEvidenceController, error) {
	if r == nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	controller, err := chatlifecycle.NewProductionEvidenceController(chatlifecycle.ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: outputDir, Observation: r.observation,
		Lifecycle: r.lifecycle, Meta: r.meta, MetaAccounting: r.accounting,
		Dataset: r.dataset, SlotAssignment: r.assignment, Continuous: continuous,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	return controller, nil
}

func (r *productionChatLifecycleRuntime) coordinator(
	cli chatLifecycleCLIConfig,
	controller *chatlifecycle.ProductionEvidenceController,
	stop <-chan struct{},
	keepWorkersRunning bool,
	continuation *chatlifecycle.CoordinatorContinuation,
) (*chatlifecycle.Coordinator, error) {
	if r == nil || controller == nil || stop == nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	generation := chatLifecycleGeneration(cli.config.Mode)
	if continuation != nil && len(continuation.Assignments) > 0 {
		generation = continuation.Assignments[0].Generation
	}
	options := chatlifecycle.CoordinatorOptions{
		Generation: generation,
		Preflight:  r.preflight, Setup: r.setup, Workers: r.workers, Observer: r.observer,
		Hooks: controller, StopRequests: stop,
		KeepWorkersRunningOnSuccess: keepWorkersRunning, Continuation: continuation,
	}
	if cli.config.Mode == chatlifecycle.ModeCapacity {
		capacityEvidence, capacityErr := chatlifecycle.NewProductionCapacityEvidence(chatlifecycle.ProductionCapacityEvidenceOptions{
			Config: cli.config, Workers: r.capacityWorkers, Observation: r.observation, Lifecycle: r.lifecycle,
		})
		if capacityErr != nil {
			return nil, errors.New("chat-lifecycle production composition failed")
		}
		options.CapacityAdmission = &chatlifecycle.CapacityAdmission{
			Reference: cli.config.Capacity.AgedCheckpoint.Reference, Checkpoint: cli.checkpoint,
		}
		options.CapacityEvidence = capacityEvidence
		options.CapacityDataset = r.dataset
	}
	coordinator, err := chatlifecycle.NewCoordinator(options)
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	return coordinator, nil
}

func composeProductionChatLifecycleRunner(cli chatLifecycleCLIConfig) (chatLifecycleCommandRunner, error) {
	if cli.config.Validate() != nil || cli.outputDir == "" {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	var runtimeSafety chatlifecycle.RuntimeSafetyGuard
	if cli.config.Stage == chatlifecycle.StageRehearsal {
		guard, err := loadFormalRuntimeEnvelope(cli.config, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		runtimeSafety = guard
	}
	runtime, err := composeProductionChatLifecycleRuntime(cli.config, runtimeSafety)
	if err != nil {
		return nil, err
	}
	controller, err := runtime.controller(cli.config, cli.outputDir, false)
	if err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	coordinator, err := runtime.coordinator(cli, controller, stop, false, nil)
	if err != nil {
		controller.Close()
		return nil, err
	}
	return &productionChatLifecycleRunner{
		cfg: cli.config, outputDir: filepath.Clean(cli.outputDir), coordinator: coordinator,
		controller: controller, stop: stop,
	}, nil
}

func (r *productionChatLifecycleRunner) Run(ctx context.Context) (chatLifecycleRunResult, error) {
	if r == nil || ctx == nil || r.coordinator == nil || r.controller == nil {
		return chatLifecycleRunResult{}, errors.New("chat-lifecycle production runner failed")
	}
	defer r.controller.Close()
	result := r.coordinator.Run(ctx, r.cfg)
	finalPath := filepath.Join(r.outputDir, "final.json")
	if report, err := chatlifecycle.ReadReport(finalPath); err == nil {
		verdict := chatlifecycle.VerdictSnapshot{
			Outcome: report.Verdict.Outcome, Cause: report.Verdict.Cause, Terminal: report.Verdict.Terminal,
			CleanupErrorCount: report.Verdict.CleanupErrorCount,
			CleanupErrors:     append([]chatlifecycle.VerdictCleanupErrorCode(nil), report.Verdict.CleanupErrors...),
		}
		return chatLifecycleRunResult{
			Verdict: verdict,
			Summary: fmt.Sprintf("chat-lifecycle outcome=%s cause=%s report=%s\n", report.Verdict.Outcome, report.Verdict.Cause, finalPath),
		}, nil
	}
	verdict := chatLifecycleCoordinatorVerdict(result)
	return chatLifecycleRunResult{
		Verdict: verdict,
		Summary: fmt.Sprintf("chat-lifecycle outcome=%s cause=%s coordinator_code=%s preflight_code=%s report=unavailable\n",
			verdict.Outcome, verdict.Cause, result.Code, result.Preflight.Code),
	}, errors.New("chat-lifecycle final report unavailable")
}

func (r *productionChatLifecycleRunner) RequestStop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stop) })
}

func chatLifecycleCoordinatorVerdict(result chatlifecycle.CoordinatorResult) chatlifecycle.VerdictSnapshot {
	verdict := chatlifecycle.VerdictSnapshot{Terminal: true}
	switch result.Outcome {
	case chatlifecycle.CoordinatorProductFailure:
		verdict.Outcome, verdict.Cause = chatlifecycle.VerdictProductFailure, chatlifecycle.VerdictCauseWorkerProduct
	case chatlifecycle.CoordinatorInfrastructureFailure:
		verdict.Outcome, verdict.Cause = chatlifecycle.VerdictInfrastructureFailure, chatlifecycle.VerdictCauseDiskExhausted
	case chatlifecycle.CoordinatorStopped:
		verdict.Outcome, verdict.Cause = chatlifecycle.VerdictOperatorStop, chatlifecycle.VerdictCauseOperatorRequested
	default:
		verdict.Outcome, verdict.Cause = chatlifecycle.VerdictHarnessInvalid, chatlifecycle.VerdictCauseInvalidObservation
	}
	return verdict
}
