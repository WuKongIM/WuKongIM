package main

import (
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	repair "github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerepair"
)

const repairStepSchemaV1 = "wukongim.chat_lifecycle.repair_step/v1"

type repairStep struct {
	Schema   string          `json:"schema"`
	State    repair.State    `json:"state"`
	Decision repair.Decision `json:"decision"`
}

func addRepairCommands(root *cobra.Command) {
	addRepairBeginCommand(root)
	addRepairCaptureCommand(root)
	addRepairObserveCommand(root)
	addRepairAbortCommand(root)
}

func addRepairAbortCommand(root *cobra.Command) {
	var statePath, observedAt, reasonValue string
	command := &cobra.Command{
		Use: "repair-abort", Short: "Seal one bounded external repair-monitor failure", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var state repair.State
			if err := readStrict(statePath, &state); err != nil {
				return repair.ErrInvalidObservation
			}
			observed, err := time.Parse(time.RFC3339, observedAt)
			if err != nil || observed.Location() != time.UTC {
				return repair.ErrInvalidObservation
			}
			next, decision, err := repair.Abort(state, observed, repair.Reason(reasonValue))
			if err != nil {
				return err
			}
			return writeRepairStep(command.OutOrStdout(), repairStep{Schema: repairStepSchemaV1, State: next, Decision: decision})
		},
	}
	command.Flags().StringVar(&statePath, "state", "", "strict current repair state JSON")
	command.Flags().StringVar(&observedAt, "observed-at", "", "trusted UTC RFC3339 failure time")
	command.Flags().StringVar(&reasonValue, "reason", "", "closed external failure reason")
	for _, name := range []string{"state", "observed-at", "reason"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func addRepairCaptureCommand(root *cobra.Command) {
	var statePath, observedAt string
	var statusPaths, snapshotPaths []string
	command := &cobra.Command{
		Use: "repair-capture", Short: "Aggregate one fenced three-worker repair observation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var state repair.State
			if err := readStrict(statePath, &state); err != nil {
				return repair.ErrInvalidObservation
			}
			observed, err := time.Parse(time.RFC3339, observedAt)
			if err != nil || observed.Location() != time.UTC || len(statusPaths) != 3 || len(snapshotPaths) != 3 {
				return repair.ErrInvalidObservation
			}
			observation := repair.Observation{
				Schema: repair.ObservationSchemaV2, RequestID: state.Candidate.RequestID,
				LeaseID: state.Candidate.LeaseID, Generation: state.Candidate.Generation, ObservedAt: observed,
			}
			seen := make(map[uint64]struct{}, 3)
			allRunning, allReady, draining := true, true, false
			var runID, assignmentID string
			var workerGeneration uint64
			for index := range statusPaths {
				var status chatlifecycle.WorkerStatus
				var snapshot chatlifecycle.WorkerSnapshot
				if readStrict(statusPaths[index], &status) != nil || readStrict(snapshotPaths[index], &snapshot) != nil ||
					status.WorkerCount != 3 || snapshot.WorkerCount != 3 || status.WorkerID != snapshot.WorkerID ||
					status.RunID == "" || status.AssignmentID == "" || status.RunID != snapshot.RunID ||
					status.AssignmentID != snapshot.AssignmentID || status.Generation == 0 || status.Generation != snapshot.Generation ||
					status.Phase != snapshot.Phase || snapshot.Sessions.Online < 0 || snapshot.Uptime <= 0 {
					return repair.ErrInvalidObservation
				}
				if _, duplicate := seen[status.WorkerID]; duplicate || status.WorkerID >= 3 {
					return repair.ErrInvalidObservation
				}
				seen[status.WorkerID] = struct{}{}
				observation.Workers[status.WorkerID] = repair.WorkerProgress{
					WorkerID: status.WorkerID, Uptime: snapshot.Uptime,
					Sent: snapshot.Messages.Sent, SendAcknowledged: snapshot.Messages.SendAcknowledged,
				}
				if index == 0 {
					runID, assignmentID, workerGeneration = status.RunID, status.AssignmentID, status.Generation
				} else if status.RunID != runID || status.AssignmentID != assignmentID || status.Generation != workerGeneration {
					return repair.ErrInvalidObservation
				}
				if status.Phase != chatlifecycle.WorkerPhaseRunning {
					allRunning = false
				}
				if !status.TrafficReady {
					allReady = false
				}
				if status.Phase == chatlifecycle.WorkerPhaseStopping || status.Phase == chatlifecycle.WorkerPhaseFinal {
					draining = true
				}
				var addErr error
				observation.Online, addErr = addRepairCounter(observation.Online, uint64(snapshot.Sessions.Online))
				if addErr == nil {
					observation.Sent, addErr = addRepairCounter(observation.Sent, snapshot.Messages.Sent)
				}
				if addErr == nil {
					observation.SendAcknowledged, addErr = addRepairCounter(observation.SendAcknowledged, snapshot.Messages.SendAcknowledged)
				}
				for _, failures := range []uint64{
					snapshot.Messages.ReceiveAckFailures, snapshot.Messages.Terminal,
					snapshot.Messages.Losses, snapshot.Messages.Duplicates,
					snapshot.Messages.Corruptions, snapshot.Messages.SequenceRegressions, snapshot.Harness.Failures,
				} {
					if addErr == nil {
						observation.TerminalErrors, addErr = addRepairCounter(observation.TerminalErrors, failures)
					}
				}
				if status.Unexpected || snapshot.Harness.UnexpectedExit {
					observation.TerminalErrors, addErr = addRepairCounter(observation.TerminalErrors, 1)
				}
				if addErr != nil {
					return repair.ErrInvalidObservation
				}
			}
			switch {
			case draining:
				observation.Phase = repair.PhaseDrain
			case allRunning && allReady:
				observation.Phase = repair.PhaseActive
			case allRunning:
				observation.Phase = repair.PhaseWarmup
			default:
				observation.Phase = repair.PhaseDeploying
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(observation)
		},
	}
	flags := command.Flags()
	flags.StringVar(&statePath, "state", "", "strict current repair state JSON")
	flags.StringVar(&observedAt, "observed-at", "", "trusted UTC RFC3339 observation time")
	flags.StringArrayVar(&statusPaths, "worker-status", nil, "one strict worker status JSON per worker")
	flags.StringArrayVar(&snapshotPaths, "worker-snapshot", nil, "one strict worker snapshot JSON per worker")
	for _, name := range []string{"state", "observed-at", "worker-status", "worker-snapshot"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func addRepairCounter(current, value uint64) (uint64, error) {
	if value > math.MaxUint64-current {
		return 0, repair.ErrInvalidObservation
	}
	return current + value, nil
}

func addRepairBeginCommand(root *cobra.Command) {
	var requestID, leaseID, sourceSHA, bundleDigest, startedAt string
	var generation, targetOnline, minimumOnlinePercent, minimumSendRate, maximumAckBacklog uint64
	var warmupTimeout, stallAfter, qualifyAfter time.Duration
	command := &cobra.Command{
		Use: "repair-begin", Short: "Create the bounded monitor state for one repair generation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			started, err := time.Parse(time.RFC3339, startedAt)
			if err != nil || started.Location() != time.UTC {
				return repair.ErrInvalidCandidate
			}
			config := repair.Config{
				TargetOnline: targetOnline, MinimumOnlinePercent: minimumOnlinePercent,
				WarmupTimeout: warmupTimeout, StallAfter: stallAfter, QualifyAfter: qualifyAfter,
				MinimumSendRatePerSecond: minimumSendRate, MaximumAckBacklog: maximumAckBacklog,
			}
			state, err := repair.Begin(config, repair.Candidate{
				RequestID: requestID, LeaseID: leaseID, Generation: generation,
				SourceSHA: sourceSHA, BundleDigest: bundleDigest,
			}, started)
			if err != nil {
				return err
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(state)
		},
	}
	flags := command.Flags()
	flags.StringVar(&requestID, "request-id", "", "exact repair request identity")
	flags.StringVar(&leaseID, "lease-id", "", "exact retained Lease identity")
	flags.Uint64Var(&generation, "generation", 0, "one-based candidate deployment generation")
	flags.StringVar(&sourceSHA, "source-sha", "", "protected-main candidate source SHA")
	flags.StringVar(&bundleDigest, "bundle-digest", "", "content-addressed candidate bundle digest")
	flags.StringVar(&startedAt, "started-at", "", "trusted UTC RFC3339 generation start")
	flags.Uint64Var(&targetOnline, "target-online", 0, "expected online session count")
	flags.Uint64Var(&minimumOnlinePercent, "minimum-online-percent", 0, "minimum stable percentage of target sessions")
	flags.Uint64Var(&minimumSendRate, "minimum-send-rate", 0, "minimum sustained logical SENDs per second")
	flags.Uint64Var(&maximumAckBacklog, "maximum-ack-backlog", 0, "maximum bounded logical SEND minus SENDACK backlog")
	flags.DurationVar(&warmupTimeout, "warmup-timeout", 0, "maximum time before active traffic must begin")
	flags.DurationVar(&stallAfter, "stall-after", 0, "continuous active-phase stall before fail-fast")
	flags.DurationVar(&qualifyAfter, "qualify-after", 0, "minimum continuously healthy active interval")
	for _, name := range []string{"request-id", "lease-id", "generation", "source-sha", "bundle-digest", "started-at", "target-online", "minimum-online-percent", "minimum-send-rate", "maximum-ack-backlog", "warmup-timeout", "stall-after", "qualify-after"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func addRepairObserveCommand(root *cobra.Command) {
	var statePath, observationPath string
	command := &cobra.Command{
		Use: "repair-observe", Short: "Advance one repair generation from an exact aggregate observation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var state repair.State
			if err := readStrict(statePath, &state); err != nil {
				return repair.ErrInvalidObservation
			}
			var observation repair.Observation
			if err := readStrict(observationPath, &observation); err != nil {
				return repair.ErrInvalidObservation
			}
			next, decision, err := repair.Advance(state.Config, state, observation)
			if err != nil {
				return err
			}
			return writeRepairStep(command.OutOrStdout(), repairStep{
				Schema: repairStepSchemaV1, State: next, Decision: decision,
			})
		},
	}
	command.Flags().StringVar(&statePath, "state", "", "strict prior repair state JSON")
	command.Flags().StringVar(&observationPath, "observation", "", "strict aggregate repair observation JSON")
	for _, name := range []string{"state", "observation"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func writeRepairStep(writer io.Writer, step repairStep) error {
	return json.NewEncoder(writer).Encode(step)
}
