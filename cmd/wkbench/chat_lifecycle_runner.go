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

const chatLifecycleCoordinatorGeneration uint64 = 1

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

func composeProductionChatLifecycleRunner(cli chatLifecycleCLIConfig) (chatLifecycleCommandRunner, error) {
	if cli.config.Validate() != nil || cli.outputDir == "" {
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

	workerInterfaces := make([]chatlifecycle.CoordinatorWorker, len(cli.config.Observation.Workers))
	var lifecycleWorkers [3]chatlifecycle.ProductionLifecycleWorker
	var capacityWorkers [3]chatlifecycle.ProductionCapacityWorker
	workerTokens := make(map[string]string, len(cli.config.Observation.Workers))
	for index, endpoint := range cli.config.Observation.Workers {
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
		APIAddrs: append([]string(nil), cli.config.Observation.APIAddrs...),
		Token:    credentials.BenchToken(), HTTPClient: httpClient,
	})
	setup, err := chatlifecycle.NewGroupSetup(chatlifecycle.GroupSetupOptions{
		Target: targetClient, MaxChannelsPerBatch: 2_000, MaxSubscribersPerBatch: 4_096,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	observation, err := chatlifecycle.NewProductionObservationSource(chatlifecycle.ProductionObservationOptions{
		Config: cli.config, BenchToken: credentials.BenchToken(), HTTPClient: httpClient,
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
	for index, endpoint := range cli.config.Observation.ServiceNodes {
		metrics[index] = target.NewClient(target.Config{
			APIAddrs: []string{endpoint.Address}, Token: credentials.BenchToken(), HTTPClient: httpClient,
		})
	}
	meta, err := chatlifecycle.NewProductionMetaController(chatlifecycle.ProductionMetaControllerOptions{
		Config: cli.config, Metrics: metrics, Accounting: accounting,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	dataset := chatlifecycle.NewCapacityDatasetProbe(chatlifecycle.CapacityDatasetProbeOptions{
		BenchToken: credentials.BenchToken(), HTTPClient: httpClient,
	})
	controller, err := chatlifecycle.NewProductionEvidenceController(chatlifecycle.ProductionEvidenceControllerOptions{
		Config: cli.config, OutputDir: cli.outputDir, Observation: observation,
		Lifecycle: lifecycle, Meta: meta, MetaAccounting: accounting,
		Dataset: dataset, SlotAssignment: assignment,
	})
	if err != nil {
		return nil, errors.New("chat-lifecycle production composition failed")
	}
	preflight := chatlifecycle.NewPreflight(chatlifecycle.PreflightOptions{
		BenchToken: credentials.BenchToken(), WorkerTokens: workerTokens, HTTPClient: httpClient,
	})
	stop := make(chan struct{})
	options := chatlifecycle.CoordinatorOptions{
		Generation: chatLifecycleCoordinatorGeneration,
		Preflight:  preflight, Setup: setup, Workers: workerInterfaces, Observer: observer,
		Hooks: controller, StopRequests: stop,
	}
	if cli.config.Mode == chatlifecycle.ModeCapacity {
		capacityEvidence, capacityErr := chatlifecycle.NewProductionCapacityEvidence(chatlifecycle.ProductionCapacityEvidenceOptions{
			Config: cli.config, Workers: capacityWorkers, Observation: observation, Lifecycle: lifecycle,
		})
		if capacityErr != nil {
			controller.Close()
			return nil, errors.New("chat-lifecycle production composition failed")
		}
		options.CapacityAdmission = &chatlifecycle.CapacityAdmission{
			Reference: cli.config.Capacity.AgedCheckpoint.Reference, Checkpoint: cli.checkpoint,
		}
		options.CapacityEvidence = capacityEvidence
		options.CapacityDataset = dataset
	}
	coordinator, err := chatlifecycle.NewCoordinator(options)
	if err != nil {
		controller.Close()
		return nil, errors.New("chat-lifecycle production composition failed")
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
