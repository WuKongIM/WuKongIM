package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	benchreport "github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/spf13/cobra"
)

type localSingleNodeStepFlags struct {
	offeredQPS            int
	requiredConnections   int
	groupMembers          int
	warmupSeconds         int
	measuredSeconds       int
	drainBudgetSeconds    int
	maximumSampleGap      float64
	scenarioPath          string
	planPath              string
	reportPath            string
	diagnosticSummaryPath string
	lifecyclePath         string
	baselineMetricsPath   string
	terminalMetricsPath   string
	storageOverlapPath    string
	storageSummaryPath    string
	hostIOSummaryPath     string
	profileStatusPath     string
	payloadRoot           string
	payloadManifestPath   string
	outputPath            string
	resultOutputPath      string
	closureOutputPath     string
	payloadComplete       bool
	checksumsVerified     bool
}

func newLocalSingleNodeStepReportCommand() *cobra.Command {
	var flags localSingleNodeStepFlags
	cmd := &cobra.Command{
		Use:   "local-single-node-step",
		Short: "Build and classify one typed single-node cluster rate-step evidence document",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			manifest, manifestErr := verifyLocalSingleNodeChecksumManifest(flags.payloadRoot, flags.payloadManifestPath)
			if manifestErr == nil {
				productExecutablePath := filepath.Join(flags.payloadRoot, "reports", fmt.Sprintf("%06d-qps", flags.offeredQPS), "evidence", "product-executable.tsv")
				for _, inputPath := range []string{
					flags.scenarioPath, flags.planPath, flags.reportPath,
					filepath.Join(flags.payloadRoot, "config", "effective-wukongim.toml"),
					filepath.Join(flags.payloadRoot, "bin", "wukongim"),
					filepath.Join(flags.payloadRoot, "bin", "wkbench"),
					productExecutablePath,
					flags.diagnosticSummaryPath, flags.lifecyclePath, flags.baselineMetricsPath,
					flags.terminalMetricsPath, flags.storageOverlapPath,
					flags.storageSummaryPath, flags.hostIOSummaryPath, flags.profileStatusPath,
				} {
					if err := manifest.requireInput(inputPath); err != nil {
						manifestErr = err
						break
					}
				}
			}
			flags.payloadComplete = manifestErr == nil
			flags.checksumsVerified = manifestErr == nil
			evidence := incompleteLocalSingleNodeStepEvidence(flags)
			var buildErr error
			if manifestErr == nil {
				evidence, buildErr = buildLocalSingleNodeStepEvidenceFromVerifiedManifest(manifest, closureManifestFromStepFlags(manifest, flags))
			}
			if manifestErr != nil {
				buildErr = errors.Join(buildErr, fmt.Errorf("payload manifest: %w", manifestErr))
			}
			result := localbaseline.CloseStepResult(evidence, manifest.digest)
			if buildErr == nil {
				if _, err := publishLocalSingleNodeStepClosure(flags, manifest, evidence, result); err != nil {
					return commandExit{code: exitInternal, message: "local single-node step closure write failed: " + err.Error()}
				}
			} else if err := publishLocalSingleNodeUnclosedStep(flags, evidence, result); err != nil {
				return commandExit{code: exitInternal, message: "local single-node step evidence write failed"}
			}
			if buildErr != nil {
				return commandExit{code: exitInternal, message: "local single-node step evidence incomplete: " + buildErr.Error()}
			}
			return exitCodeError(localSingleNodeStepExitCode(result))
		},
	}
	cmd.Flags().IntVar(&flags.offeredQPS, "offered-qps", 0, "offered measured SEND/s")
	cmd.Flags().IntVar(&flags.requiredConnections, "required-active-connections", 0, "required live WKProto sessions")
	cmd.Flags().IntVar(&flags.groupMembers, "group-members", 0, "configured members per reviewed group channel")
	cmd.Flags().IntVar(&flags.warmupSeconds, "warmup-seconds", 0, "configured warmup seconds")
	cmd.Flags().IntVar(&flags.measuredSeconds, "measured-seconds", 0, "configured measured seconds")
	cmd.Flags().IntVar(&flags.drainBudgetSeconds, "drain-budget-seconds", 0, "configured drain budget seconds")
	cmd.Flags().Float64Var(&flags.maximumSampleGap, "maximum-sample-gap-seconds", 0, "largest accepted lifecycle observation gap")
	cmd.Flags().StringVar(&flags.scenarioPath, "scenario", "", "effective scenario.yaml executed by the coordinator")
	cmd.Flags().StringVar(&flags.planPath, "plan", "", "deterministic plan.json executed by the coordinator")
	cmd.Flags().StringVar(&flags.reportPath, "run-report", "", "canonical report.json containing the executed scenario and plan")
	cmd.Flags().StringVar(&flags.diagnosticSummaryPath, "diagnostic-summary", "", "wkbench diagnostic-summary.json")
	cmd.Flags().StringVar(&flags.lifecyclePath, "lifecycle", "", "periodic worker/process JSONL")
	cmd.Flags().StringVar(&flags.baselineMetricsPath, "post-warmup-metrics", "", "raw post-warmup Prometheus cut")
	cmd.Flags().StringVar(&flags.terminalMetricsPath, "terminal-metrics", "", "raw post-drain Prometheus cut")
	cmd.Flags().StringVar(&flags.storageOverlapPath, "storage-overlap", "", "raw snapshot/compaction overlap TSV")
	cmd.Flags().StringVar(&flags.storageSummaryPath, "storage-summary", "", "per-step storage metrics summary TSV")
	cmd.Flags().StringVar(&flags.hostIOSummaryPath, "host-io-summary", "", "per-step host block I/O summary TSV")
	cmd.Flags().StringVar(&flags.profileStatusPath, "profile-status", "", "closed threshold-only pprof status JSON")
	cmd.Flags().StringVar(&flags.payloadRoot, "payload-root", "", "root directory containing the closed step payload")
	cmd.Flags().StringVar(&flags.payloadManifestPath, "payload-manifest", "", "verified checksum manifest for the closed step payload")
	cmd.Flags().StringVar(&flags.outputPath, "output", "", "typed step evidence JSON")
	cmd.Flags().StringVar(&flags.resultOutputPath, "result-output", "", "typed closed step result JSON")
	cmd.Flags().StringVar(&flags.closureOutputPath, "closure-output", "", "atomic step closure manifest JSON")
	for _, name := range []string{
		"offered-qps", "required-active-connections", "group-members", "warmup-seconds", "measured-seconds",
		"drain-budget-seconds", "maximum-sample-gap-seconds", "scenario", "plan", "run-report", "diagnostic-summary", "lifecycle",
		"post-warmup-metrics", "terminal-metrics", "storage-overlap", "storage-summary", "host-io-summary", "profile-status", "payload-root", "payload-manifest",
		"output", "result-output", "closure-output",
	} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func incompleteLocalSingleNodeStepEvidence(flags localSingleNodeStepFlags) localbaseline.StepEvidence {
	return localbaseline.BuildStepEvidence(localbaseline.StepCaptureInput{
		OfferedSendQPS: flags.offeredQPS, RequiredActiveConnections: flags.requiredConnections,
		ConfiguredGroupMembers:  flags.groupMembers,
		ConfiguredWarmupSeconds: flags.warmupSeconds, ConfiguredMeasuredSeconds: flags.measuredSeconds,
		ConfiguredDrainBudgetSeconds: flags.drainBudgetSeconds, MaximumSampleGapSeconds: flags.maximumSampleGap,
		Seal: localbaseline.SealEvidence{},
	})
}

func closureManifestFromStepFlags(manifest localSingleNodeVerifiedManifest, flags localSingleNodeStepFlags) localbaseline.StepClosureManifest {
	relative := func(value string) string {
		got, _ := manifest.artifactRoot.relative(value)
		return got
	}
	return localbaseline.StepClosureManifest{
		Schema: localbaseline.StepClosureManifestSchema,
		Inputs: localbaseline.StepClosureInputs{
			Scenario: relative(flags.scenarioPath), Plan: relative(flags.planPath), Report: relative(flags.reportPath),
			EffectiveConfig: "config/effective-wukongim.toml", WukongIMBinary: "bin/wukongim", WkbenchBinary: "bin/wkbench",
			ProductExecutable: fmt.Sprintf("reports/%06d-qps/evidence/product-executable.tsv", flags.offeredQPS),
			DiagnosticSummary: relative(flags.diagnosticSummaryPath), Lifecycle: relative(flags.lifecyclePath),
			PostWarmupMetrics: relative(flags.baselineMetricsPath), TerminalMetrics: relative(flags.terminalMetricsPath),
			StorageOverlap: relative(flags.storageOverlapPath), StorageSummary: relative(flags.storageSummaryPath),
			HostIOSummary: relative(flags.hostIOSummaryPath), ProfileStatus: relative(flags.profileStatusPath),
		},
		Settings: localbaseline.StepClosureSettings{
			OfferedSendQPS: flags.offeredQPS, RequiredActiveConnections: flags.requiredConnections,
			ConfiguredGroupMembers:  flags.groupMembers,
			ConfiguredWarmupSeconds: flags.warmupSeconds, ConfiguredMeasuredSeconds: flags.measuredSeconds,
			ConfiguredDrainBudgetSeconds: flags.drainBudgetSeconds, MaximumSampleGapSeconds: flags.maximumSampleGap,
		},
	}
}

func buildLocalSingleNodeStepEvidenceFromVerifiedManifest(
	manifest localSingleNodeVerifiedManifest,
	closure localbaseline.StepClosureManifest,
) (localbaseline.StepEvidence, error) {
	input := localbaseline.StepCaptureInput{
		OfferedSendQPS: closure.Settings.OfferedSendQPS, RequiredActiveConnections: closure.Settings.RequiredActiveConnections,
		ConfiguredGroupMembers:       closure.Settings.ConfiguredGroupMembers,
		ConfiguredWarmupSeconds:      closure.Settings.ConfiguredWarmupSeconds,
		ConfiguredMeasuredSeconds:    closure.Settings.ConfiguredMeasuredSeconds,
		ConfiguredDrainBudgetSeconds: closure.Settings.ConfiguredDrainBudgetSeconds,
		MaximumSampleGapSeconds:      closure.Settings.MaximumSampleGapSeconds,
		Seal:                         localbaseline.SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
	}
	var errs []error
	var reviewed localSingleNodeReviewedExecution
	reviewedValid := false
	scenarioData, scenarioErr := manifest.bytesForRelative(closure.Inputs.Scenario)
	planData, planErr := manifest.bytesForRelative(closure.Inputs.Plan)
	reportData, reportErr := manifest.bytesForRelative(closure.Inputs.Report)
	if scenarioErr != nil || planErr != nil || reportErr != nil {
		errs = append(errs, fmt.Errorf("effective scenario/plan unavailable"))
	} else if parsed, reviewedErr := parseLocalSingleNodeReviewedExecution(scenarioData, planData, reportData); reviewedErr != nil {
		errs = append(errs, fmt.Errorf("effective scenario/plan: %w", reviewedErr))
	} else if !localSingleNodeReviewedExecutionMatchesSettings(parsed, closure.Settings) {
		errs = append(errs, fmt.Errorf("effective scenario/plan does not match closed step settings"))
	} else {
		reviewed = parsed
		reviewedValid = true
		// The executed, coordinator-emitted scenario is authoritative. CLI values
		// are retained in the closure only so replay can prove exact equality.
		input.OfferedSendQPS = reviewed.OfferedSendQPS
		input.RequiredActiveConnections = reviewed.RequiredActiveConnections
		input.ConfiguredGroupMembers = reviewed.GroupMembers
		input.ConfiguredWarmupSeconds = reviewed.WarmupSeconds
		input.ConfiguredMeasuredSeconds = reviewed.MeasuredSeconds
		input.ConfiguredDrainBudgetSeconds = reviewed.DrainBudgetSeconds
		input.Target = reviewed.Target
		input.ExecutionSeal = localbaseline.ExecutionSealEvidence{
			BaselineInvocationID:  reviewed.BaselineInvocationID,
			EffectiveConfigSHA256: manifest.entries[closure.Inputs.EffectiveConfig],
			WukongIMBinarySHA256:  manifest.entries[closure.Inputs.WukongIMBinary],
			WkbenchBinarySHA256:   manifest.entries[closure.Inputs.WkbenchBinary],
		}
	}
	attestationData, attestationErr := manifest.boundedBytesForRelative(
		closure.Inputs.ProductExecutable, localSingleNodeMaximumExecutableAttestBytes,
	)
	if attestationErr != nil {
		errs = append(errs, fmt.Errorf("product executable attestation: %w", attestationErr))
	} else if !reviewedValid {
		errs = append(errs, fmt.Errorf("product executable attestation has no reviewed execution identity"))
	} else {
		attestation, parseErr := parseLocalSingleNodeProductExecutableAttestation(
			attestationData, reviewed.BaselineInvocationID, reviewed.OfferedSendQPS,
			manifest.entries[closure.Inputs.WukongIMBinary],
		)
		if parseErr != nil {
			errs = append(errs, parseErr)
		} else {
			input.ExecutionSeal.SourceConfigSHA256 = attestation.SourceConfigSHA256
		}
	}
	var diagnostic benchreport.DiagnosticSummary
	diagnosticData, err := manifest.bytesForRelative(closure.Inputs.DiagnosticSummary)
	if err == nil {
		err = decodeLocalSingleNodeStrictJSON(diagnosticData, &diagnostic)
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("diagnostic summary: %w", err))
	} else if reviewedValid && !localSingleNodeReviewedExecutionMatchesDiagnostic(reviewed, diagnostic) {
		errs = append(errs, fmt.Errorf("diagnostic summary run identity or report status does not match canonical run report"))
	} else {
		input.RunID = diagnostic.RunID
		input.PhaseWindows = make([]localbaseline.PhaseWindow, 0, len(diagnostic.PhaseWindows))
		for _, window := range diagnostic.PhaseWindows {
			input.PhaseWindows = append(input.PhaseWindows, localbaseline.PhaseWindow{
				Phase: window.Phase, StartedAt: window.StartedAt, EndedAt: window.EndedAt,
			})
		}
	}
	lifecycleData, err := manifest.bytesForRelative(closure.Inputs.Lifecycle)
	if err != nil {
		errs = append(errs, fmt.Errorf("lifecycle timeline: %w", err))
	} else {
		input.Lifecycle, err = localbaseline.ParseLifecycleCaptures(bytes.NewReader(lifecycleData))
		if err != nil {
			errs = append(errs, err)
		}
	}
	baselineData, baselineErr := manifest.bytesForRelative(closure.Inputs.PostWarmupMetrics)
	terminalData, terminalErr := manifest.bytesForRelative(closure.Inputs.TerminalMetrics)
	if baselineErr != nil || terminalErr != nil {
		errs = append(errs, fmt.Errorf("product queue cuts unavailable"))
	} else {
		input.ProductQueues, err = localbaseline.BuildProductQueueEvidence(bytes.NewReader(baselineData), bytes.NewReader(terminalData))
		if err != nil {
			errs = append(errs, err)
		}
	}
	storageData, storageErr := manifest.bytesForRelative(closure.Inputs.StorageOverlap)
	storageDirectory := path.Dir(closure.Inputs.StorageOverlap)
	if storageErr == nil {
		input.StorageOverlap, err = localbaseline.ParseStorageOverlapEvidence(bytes.NewReader(storageData), input.RunID,
			func(relative string, maximum int64) ([]byte, error) {
				return manifest.boundedBytesForRelative(path.Join(storageDirectory, relative), maximum)
			})
	} else {
		err = storageErr
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("storage overlap: %w", err))
	}
	storageSummaryData, storageSummaryErr := manifest.bytesForRelative(closure.Inputs.StorageSummary)
	if storageSummaryErr == nil {
		expectedNode := strings.TrimSuffix(path.Base(closure.Inputs.PostWarmupMetrics), "-post-warmup.prom")
		if expectedNode == path.Base(closure.Inputs.PostWarmupMetrics) || expectedNode == "" {
			storageSummaryErr = errors.New("post-warmup metrics path does not identify a node")
		} else {
			input.StorageMetrics, storageSummaryErr = localbaseline.ParseStorageMetricsSummary(
				bytes.NewReader(storageSummaryData), fmt.Sprintf("%06d", closure.Settings.OfferedSendQPS), expectedNode,
			)
		}
	}
	if storageSummaryErr != nil {
		errs = append(errs, fmt.Errorf("storage metrics summary: %w", storageSummaryErr))
	}
	hostSummaryData, hostSummaryErr := manifest.bytesForRelative(closure.Inputs.HostIOSummary)
	if hostSummaryErr == nil {
		input.HostIO, hostSummaryErr = localbaseline.ParseHostIOSummary(
			bytes.NewReader(hostSummaryData), fmt.Sprintf("%06d", closure.Settings.OfferedSendQPS), "host-local",
		)
	}
	if hostSummaryErr != nil {
		errs = append(errs, fmt.Errorf("host I/O summary: %w", hostSummaryErr))
	}
	profileData, profileErr := manifest.bytesForRelative(closure.Inputs.ProfileStatus)
	profileDirectory := path.Dir(closure.Inputs.ProfileStatus)
	if profileErr == nil {
		input.Profile, err = localbaseline.ParseSingleNodeProfileEvidence(bytes.NewReader(profileData),
			func(relative string, maximum int64) ([]byte, error) {
				data, readErr := manifest.boundedBytesForRelative(path.Join(profileDirectory, relative), maximum)
				if readErr != nil && strings.Contains(readErr.Error(), "absent") {
					return nil, localbaseline.ErrAuthenticatedArtifactMissing
				}
				return data, readErr
			})
	} else {
		err = profileErr
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("threshold profile: %w", err))
	} else {
		profileQuery := localbaseline.QueryFirstMeasuredProfileThreshold(input.Lifecycle, input.RunID, closure.Settings.OfferedSendQPS, 90)
		if !localbaseline.ProfileEvidenceMatchesQuery(input.Profile, profileQuery) {
			errs = append(errs, errors.New("threshold profile does not match the first typed measured threshold"))
		}
	}
	evidence := localbaseline.BuildStepEvidence(input)
	if len(errs) > 0 {
		evidence.Timeline.CaptureComplete = false
		evidence.ProductQueues.BoundaryEvidenceComplete = false
		evidence.StorageOverlap.CaptureComplete = false
		evidence.Seal.PayloadComplete = false
	}
	return evidence, errors.Join(errs...)
}

func localSingleNodeReviewedExecutionMatchesDiagnostic(reviewed localSingleNodeReviewedExecution, diagnostic benchreport.DiagnosticSummary) bool {
	return reviewed.RunID == diagnostic.RunID &&
		reviewed.ReportStatus == diagnostic.Status &&
		reviewed.ReportExitCode == diagnostic.ExitCode &&
		reviewed.ReportStabilityVerdict == diagnostic.StabilityVerdict
}

func localSingleNodeReviewedExecutionMatchesSettings(reviewed localSingleNodeReviewedExecution, settings localbaseline.StepClosureSettings) bool {
	return reviewed.OfferedSendQPS == settings.OfferedSendQPS &&
		reviewed.RequiredActiveConnections == settings.RequiredActiveConnections &&
		reviewed.GroupMembers == settings.ConfiguredGroupMembers &&
		reviewed.WarmupSeconds == settings.ConfiguredWarmupSeconds &&
		reviewed.MeasuredSeconds == settings.ConfiguredMeasuredSeconds &&
		reviewed.DrainBudgetSeconds == settings.ConfiguredDrainBudgetSeconds
}

func localSingleNodeProfilePayloadPaths(statusPath string, evidence localbaseline.ProfileEvidence) []string {
	paths := []string{statusPath}
	if !evidence.Triggered {
		return paths
	}
	profileRoot := filepath.Join(filepath.Dir(statusPath), "threshold-pprof")
	paths = append(paths, filepath.Join(profileRoot, "metadata.json"))
	if evidence.Status == "complete" {
		paths = append(paths,
			filepath.Join(profileRoot, "profiles", "node-1-cpu.pb.gz"),
			filepath.Join(profileRoot, "profiles", "node-1-heap.pb.gz"),
			filepath.Join(profileRoot, "profiles", "node-1-goroutine.txt"),
		)
	}
	return paths
}

func localSingleNodeStepExitCode(result localbaseline.ClosedStepResult) int {
	if result.Clean {
		return 0
	}
	if result.Outcome == localbaseline.OutcomeRateFailed || result.Outcome == localbaseline.OutcomeProductFailure {
		return exitHardLimit
	}
	return exitInternal
}

func readLocalSingleNodeJSON(path string, out any) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("trailing JSON document")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func writeLocalSingleNodeJSON(path string, value any) error {
	path = filepath.Clean(path)
	if path == "." || filepath.Base(path) == "." {
		return fmt.Errorf("output path is invalid")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".local-single-node-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	complete := false
	defer func() {
		_ = temporary.Close()
		if !complete {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	complete = true
	return nil
}
