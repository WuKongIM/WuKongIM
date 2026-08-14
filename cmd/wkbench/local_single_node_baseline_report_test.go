package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	benchreport "github.com/WuKongIM/WuKongIM/internal/bench/report"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"gopkg.in/yaml.v3"
)

func TestLocalSingleNodeBaselineReportCommandWritesAuthorization(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(dir, "evidence-draft.json")
	sealedEvidencePath := filepath.Join(dir, "reports", "local-baseline-evidence.json")
	resultPath := filepath.Join(dir, "authorization.json")
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = nil
	evidence.CompletionGeneration = ""
	writeLocalSingleNodeEvidenceFixture(t, evidencePath, evidence)
	closures := writeLocalSingleNodeBaselineClosureFixtures(t, dir, evidence.Settings, -1)
	var stderr bytes.Buffer

	args := []string{
		"report", "local-single-node-baseline",
		"--root", dir, "--evidence", evidencePath, "--sealed-evidence-output", sealedEvidencePath,
		"--output", resultPath,
	}
	for _, closure := range closures {
		args = append(args, "--step-closure", closure)
	}
	code := runWithStderr(args, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr.String())
	}
	var result localbaseline.AuthorizationResult
	decodeLocalSingleNodeJSON(t, resultPath, &result)
	if !result.Authorizes || !result.ReviewedContractSatisfied {
		t.Fatalf("authorization = %+v, want authorized", result)
	}
}

func TestLocalSingleNodeBaselineReportRejectsResealedMixedSourceConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(dir, "evidence-draft.json")
	sealedEvidencePath := filepath.Join(dir, "reports", "local-baseline-evidence.json")
	resultPath := filepath.Join(dir, "authorization.json")
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = nil
	evidence.CompletionGeneration = ""
	writeLocalSingleNodeEvidenceFixture(t, evidencePath, evidence)
	closures := writeLocalSingleNodeBaselineClosureFixtures(t, dir, evidence.Settings, -1)

	attestationPath := filepath.Join(dir, "reports", "000500-qps", "evidence", "product-executable.tsv")
	attestation, err := os.ReadFile(attestationPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(attestation),
		"source_config_sha256\t"+strings.Repeat("d", 64),
		"source_config_sha256\t"+strings.Repeat("e", 64), 1,
	)
	if changed == string(attestation) {
		t.Fatal("fixture source config digest was not changed")
	}
	if err := os.WriteFile(attestationPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "reports", "000500-qps", "evidence", "step-checksums.sha256")
	writeLocalSingleNodeChecksumManifest(t, dir, manifestPath, localSingleNodeManifestEntriesFixture(t, manifestPath))
	if code, stderr := rerunLocalSingleNodeStepFixture(t, dir, 500); code != 0 {
		t.Fatalf("resealed independent step exit/stderr = %d/%q", code, stderr)
	}

	args := []string{
		"report", "local-single-node-baseline",
		"--root", dir, "--evidence", evidencePath, "--sealed-evidence-output", sealedEvidencePath,
		"--output", resultPath,
	}
	for _, closure := range closures {
		args = append(args, "--step-closure", closure)
	}
	var stderr bytes.Buffer
	if code := runWithStderr(args, &stderr); code != exitInternal {
		t.Fatalf("mixed source config exit/stderr = %d/%q", code, stderr.String())
	}
	var result localbaseline.AuthorizationResult
	decodeLocalSingleNodeJSON(t, resultPath, &result)
	foundExecutionSeal := false
	for _, reason := range result.Reasons {
		foundExecutionSeal = foundExecutionSeal || reason == localbaseline.AuthorizationReasonExecutionSeal
	}
	if result.Authorizes || !foundExecutionSeal {
		t.Fatalf("mixed source config authorization = %+v", result)
	}
}

func TestLocalSingleNodeStepReportCommandBuildsClosedRawEvidence(t *testing.T) {
	dir := t.TempDir()
	diagnosticPath := filepath.Join(dir, "diagnostic-summary.json")
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	planPath := filepath.Join(dir, "plan.json")
	reportPath := filepath.Join(dir, "report.json")
	lifecyclePath := filepath.Join(dir, "lifecycle.jsonl")
	baselineMetricsPath := filepath.Join(dir, "127_0_0_1_5001-post-warmup.prom")
	terminalMetricsPath := filepath.Join(dir, "terminal.prom")
	storageOverlapPath := filepath.Join(dir, "evidence", "storage-overlap.tsv")
	storageSummaryPath := filepath.Join(dir, "evidence", "storage-summary.tsv")
	hostIOSummaryPath := filepath.Join(dir, "evidence", "host-io-summary.tsv")
	profileStatusPath := filepath.Join(dir, "evidence", "threshold-pprof-status.json")
	outputPath := filepath.Join(dir, "typed-step.json")
	resultPath := filepath.Join(dir, "typed-step-result.json")
	closurePath := filepath.Join(dir, "step-closure.json")
	manifestPath := filepath.Join(dir, "step-checksums.sha256")
	settings := localSingleNodeEvidenceFixture().Settings
	step := localSingleNodeStepFixture(1000, settings)
	writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
	writeLocalSingleNodeDiagnosticFixture(t, diagnosticPath, step)
	writeLocalSingleNodeLifecycleFixture(t, lifecyclePath, step)
	baselineMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.PostWarmupCut)
	terminalMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.TerminalCut)
	if err := os.WriteFile(baselineMetricsPath, []byte(baselineMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(terminalMetricsPath, []byte(terminalMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLocalSingleNodeStorageOverlapFixture(t, storageOverlapPath, step.StorageOverlap)
	writeLocalSingleNodeSummaryFixtures(t, storageSummaryPath, hostIOSummaryPath, 1000)
	writeLocalSingleNodeNotTriggeredProfileFixture(t, profileStatusPath)
	manifestEntries := []string{
		"scenario.yaml", "plan.json", "report.json", "diagnostic-summary.json", "lifecycle.jsonl", "127_0_0_1_5001-post-warmup.prom", "terminal.prom",
		"evidence/storage-overlap.tsv", "evidence/storage-summary.tsv", "evidence/host-io-summary.tsv",
		"evidence/threshold-pprof-status.json",
	}
	manifestEntries = append(manifestEntries, localSingleNodeStorageManifestPaths(step.StorageOverlap)...)
	manifestEntries = append(manifestEntries, ensureLocalSingleNodeExecutionPayloadFixture(t, dir, 1000)...)
	writeLocalSingleNodeChecksumManifest(t, dir, manifestPath, manifestEntries)
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-step",
		"--offered-qps", "1000", "--required-active-connections", "2500",
		"--group-members", "10",
		"--warmup-seconds", "60", "--measured-seconds", "300", "--drain-budget-seconds", "90",
		"--maximum-sample-gap-seconds", "30", "--scenario", scenarioPath, "--plan", planPath, "--run-report", reportPath, "--diagnostic-summary", diagnosticPath,
		"--lifecycle", lifecyclePath, "--post-warmup-metrics", baselineMetricsPath,
		"--terminal-metrics", terminalMetricsPath, "--storage-overlap", storageOverlapPath,
		"--storage-summary", storageSummaryPath, "--host-io-summary", hostIOSummaryPath,
		"--profile-status", profileStatusPath,
		"--payload-root", dir, "--payload-manifest", manifestPath,
		"--output", outputPath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)

	if code != 0 {
		var decision localbaseline.ClosedStepResult
		var built localbaseline.StepEvidence
		decodeLocalSingleNodeJSON(t, resultPath, &decision)
		decodeLocalSingleNodeJSON(t, outputPath, &built)
		t.Fatalf("exit = %d, want 0; stderr = %q; decision = %+v; terminal = %+v; storage digest = %q; product digest = %q", code, stderr.String(), decision, built.Timeline.Terminal.TerminalCut, built.StorageOverlap.PayloadSHA256, built.ProductQueues.TerminalPayloadSHA256)
	}
	var got localbaseline.StepEvidence
	decodeLocalSingleNodeJSON(t, outputPath, &got)
	if result := localbaseline.EvaluateStep(got); !result.Clean {
		t.Fatalf("built step = %+v, result = %+v", got, result)
	}
	if got.ExecutionSeal.SourceConfigSHA256 != strings.Repeat("d", 64) {
		t.Fatalf("source config execution seal = %q, want attested source digest", got.ExecutionSeal.SourceConfigSHA256)
	}
	var decision localbaseline.ClosedStepResult
	decodeLocalSingleNodeJSON(t, resultPath, &decision)
	if decision.Schema != localbaseline.ClosedStepResultSchema || !decision.Clean ||
		decision.Outcome != localbaseline.OutcomeClean || len(decision.PayloadManifestSHA256) != 64 {
		t.Fatalf("closed step result = %+v", decision)
	}
	if _, err := os.Stat(closurePath); err != nil {
		t.Fatalf("step closure was not published: %v", err)
	}
}

func TestLocalSingleNodeReviewedExecutionBindsCanonicalArtifactsAndSettings(t *testing.T) {
	dir := t.TempDir()
	settings := localSingleNodeEvidenceFixture().Settings
	settings.GroupMembers = 9
	step := localSingleNodeStepFixture(1000, settings)
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	planPath := filepath.Join(dir, "plan.json")
	reportPath := filepath.Join(dir, "report.json")
	writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	reviewed, err := parseLocalSingleNodeReviewedExecution(read(scenarioPath), read(planPath), read(reportPath))
	if err != nil {
		t.Fatalf("parse reviewed execution: %v", err)
	}
	if reviewed.GroupMembers != 9 || reviewed.OfferedSendQPS != 1000 {
		t.Fatalf("reviewed execution = %+v, want members=9 qps=1000", reviewed)
	}
	diagnostic := benchreport.DiagnosticSummary{
		RunID: reviewed.RunID, Status: reviewed.ReportStatus, ExitCode: reviewed.ReportExitCode,
		StabilityVerdict: reviewed.ReportStabilityVerdict,
	}
	if !localSingleNodeReviewedExecutionMatchesDiagnostic(reviewed, diagnostic) {
		t.Fatal("canonical report projection should match its diagnostic summary")
	}
	diagnostic.Status = benchreport.StatusFailed
	if localSingleNodeReviewedExecutionMatchesDiagnostic(reviewed, diagnostic) {
		t.Fatal("caller-resealed diagnostic status must not override the canonical run report")
	}
	closed := localbaseline.StepClosureSettings{
		OfferedSendQPS: 1000, RequiredActiveConnections: settings.ActiveConnections,
		ConfiguredGroupMembers: 9, ConfiguredWarmupSeconds: settings.WarmupSeconds,
		ConfiguredMeasuredSeconds: settings.MeasuredSeconds, ConfiguredDrainBudgetSeconds: settings.DrainBudgetSeconds,
		MaximumSampleGapSeconds: 30,
	}
	if !localSingleNodeReviewedExecutionMatchesSettings(reviewed, closed) {
		t.Fatal("canonical reviewed execution should match its closed settings")
	}
	closed.ConfiguredGroupMembers = 10
	if localSingleNodeReviewedExecutionMatchesSettings(reviewed, closed) {
		t.Fatal("caller-authored group_members must not override the executed scenario")
	}
	if _, err := parseLocalSingleNodeReviewedExecution(append(read(scenarioPath), '\n'), read(planPath), read(reportPath)); err == nil {
		t.Fatal("non-canonical scenario bytes should be rejected")
	}
}

func TestLocalSingleNodeReviewedExecutionRejectsUnreviewedScenarioAndReportShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*benchreport.Report)
	}{
		{
			name: "hash slot spread",
			mutate: func(report *benchreport.Report) {
				report.Scenario.Channels.Profiles[0].Shard.HashSlotSpread = true
			},
		},
		{
			name: "hash slot count",
			mutate: func(report *benchreport.Report) {
				report.Scenario.Channels.Profiles[0].Shard.HashSlotCount = 256
			},
		},
		{
			name: "extra plan worker",
			mutate: func(report *benchreport.Report) {
				report.Plan.Workers["worker-b"] = report.Plan.Workers["worker-a"]
			},
		},
		{
			name: "extra report worker",
			mutate: func(report *benchreport.Report) {
				report.Workers.Workers = append(report.Workers.Workers, benchmodel.Worker{
					ID: "worker-b", Addr: "http://127.0.0.1:19131", Weight: 1, InsecureControl: true,
				})
			},
		},
		{
			name: "report worker identity",
			mutate: func(report *benchreport.Report) {
				report.Workers.Workers[0].ID = "worker-b"
			},
		},
		{
			name: "extra target endpoint",
			mutate: func(report *benchreport.Report) {
				report.Target.API.Addrs = append(report.Target.API.Addrs, "http://127.0.0.1:5002")
			},
		},
		{
			name: "remote target endpoints",
			mutate: func(report *benchreport.Report) {
				report.Target.API.Addrs[0] = "http://192.0.2.10:5001"
				report.Target.BenchAPI.Addrs[0] = report.Target.API.Addrs[0]
				report.Target.Metrics.Addrs[0] = report.Target.API.Addrs[0]
				report.Target.Gateway.TCP.Addrs[0] = "192.0.2.10:5100"
			},
		},
		{
			name: "remote worker endpoint",
			mutate: func(report *benchreport.Report) {
				report.Workers.Workers[0].Addr = "http://192.0.2.11:19130"
			},
		},
		{
			name: "retained bench credential",
			mutate: func(report *benchreport.Report) {
				report.Target.BenchAPI.Token = "must-not-be-sealed"
			},
		},
		{
			name: "retained nested worker credential",
			mutate: func(report *benchreport.Report) {
				report.WorkerReports = []benchreport.WorkerReport{{
					WorkerID: "worker-a",
					Report:   json.RawMessage(`{"nested":{"api_token":"must-not-be-sealed"}}`),
				}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			settings := localSingleNodeEvidenceFixture().Settings
			step := localSingleNodeStepFixture(1000, settings)
			scenarioPath := filepath.Join(dir, "scenario.yaml")
			planPath := filepath.Join(dir, "plan.json")
			reportPath := filepath.Join(dir, "report.json")
			writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
			var report benchreport.Report
			decodeLocalSingleNodeJSON(t, reportPath, &report)
			test.mutate(&report)
			writeLocalSingleNodeCanonicalExecutionFixture(t, scenarioPath, planPath, reportPath, report)
			if _, err := parseLocalSingleNodeReviewedExecution(
				readLocalSingleNodeTestFile(t, scenarioPath),
				readLocalSingleNodeTestFile(t, planPath),
				readLocalSingleNodeTestFile(t, reportPath),
			); err == nil {
				t.Fatal("unreviewed scenario/report shape was accepted")
			}
		})
	}
}

func TestLocalSingleNodeStepReportRejectsResealedCrossRunReviewedArtifacts(t *testing.T) {
	root := t.TempDir()
	settings := localSingleNodeEvidenceFixture().Settings
	writeLocalSingleNodeCompletionStepFixture(t, root, 250, settings)
	directory := filepath.Join(root, "reports", "000250-qps")
	evidenceDirectory := filepath.Join(directory, "evidence")
	scenarioPath := filepath.Join(directory, "scenario.yaml")
	planPath := filepath.Join(directory, "plan.json")
	reportPath := filepath.Join(directory, "report.json")
	diagnosticPath := filepath.Join(directory, "diagnostic-summary.json")

	var report benchreport.Report
	decodeLocalSingleNodeJSON(t, reportPath, &report)
	report.RunID = "cross-run"
	report.Scenario.Run.ID = report.RunID
	report.Plan.RunID = report.RunID
	scenarioData, err := yaml.Marshal(report.Scenario)
	if err != nil {
		t.Fatal(err)
	}
	planData, err := json.MarshalIndent(report.Plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	reportData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		scenarioPath: scenarioData,
		planPath:     append(planData, '\n'),
		reportPath:   append(reportData, '\n'),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := filepath.Join(evidenceDirectory, "step-checksums.sha256")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifestEntries []string
	for _, line := range strings.Split(strings.TrimSpace(string(manifestData)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed fixture manifest line %q", line)
		}
		manifestEntries = append(manifestEntries, parts[1])
	}
	writeLocalSingleNodeChecksumManifest(t, root, manifestPath, manifestEntries)

	outputPath := filepath.Join(evidenceDirectory, "typed-step-evidence.json")
	resultPath := filepath.Join(evidenceDirectory, "typed-step-result.json")
	closurePath := filepath.Join(evidenceDirectory, "step-closure.json")
	for _, path := range []string{outputPath, resultPath, closurePath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step", "--offered-qps", "250",
		"--required-active-connections", "2500", "--group-members", "10", "--warmup-seconds", "60",
		"--measured-seconds", "300", "--drain-budget-seconds", "90", "--maximum-sample-gap-seconds", "30",
		"--scenario", scenarioPath, "--plan", planPath, "--run-report", reportPath, "--diagnostic-summary", diagnosticPath,
		"--lifecycle", filepath.Join(evidenceDirectory, "lifecycle.jsonl"),
		"--post-warmup-metrics", filepath.Join(evidenceDirectory, "127_0_0_1_5001-post-warmup.prom"),
		"--terminal-metrics", filepath.Join(evidenceDirectory, "terminal.prom"),
		"--storage-overlap", filepath.Join(evidenceDirectory, "storage-overlap.tsv"),
		"--storage-summary", filepath.Join(evidenceDirectory, "storage-summary.tsv"),
		"--host-io-summary", filepath.Join(evidenceDirectory, "host-io-summary.tsv"),
		"--profile-status", filepath.Join(evidenceDirectory, "threshold-pprof-status.json"),
		"--payload-root", root, "--payload-manifest", manifestPath,
		"--output", outputPath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "invocation identity") {
		t.Fatalf("cross-run reseal exit/stderr = %d/%q", code, stderr.String())
	}
	if _, err := os.Lstat(closurePath); !os.IsNotExist(err) {
		t.Fatalf("cross-run reseal published a closure: %v", err)
	}
}

func TestLocalSingleNodeStepReportRejectsMissingProductExecutableAttestation(t *testing.T) {
	root := t.TempDir()
	settings := localSingleNodeEvidenceFixture().Settings
	writeLocalSingleNodeCompletionStepFixture(t, root, 250, settings)
	directory := filepath.Join(root, "reports", "000250-qps")
	evidenceDirectory := filepath.Join(directory, "evidence")
	manifestPath := filepath.Join(evidenceDirectory, "step-checksums.sha256")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(manifestData)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed fixture manifest line %q", line)
		}
		if parts[1] != "reports/000250-qps/evidence/product-executable.tsv" {
			entries = append(entries, parts[1])
		}
	}
	writeLocalSingleNodeChecksumManifest(t, root, manifestPath, entries)

	outputPath := filepath.Join(evidenceDirectory, "typed-step-evidence.json")
	resultPath := filepath.Join(evidenceDirectory, "typed-step-result.json")
	closurePath := filepath.Join(evidenceDirectory, "step-closure.json")
	for _, path := range []string{outputPath, resultPath, closurePath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step", "--offered-qps", "250",
		"--required-active-connections", "2500", "--group-members", "10", "--warmup-seconds", "60",
		"--measured-seconds", "300", "--drain-budget-seconds", "90", "--maximum-sample-gap-seconds", "30",
		"--scenario", filepath.Join(directory, "scenario.yaml"),
		"--plan", filepath.Join(directory, "plan.json"),
		"--run-report", filepath.Join(directory, "report.json"),
		"--diagnostic-summary", filepath.Join(directory, "diagnostic-summary.json"),
		"--lifecycle", filepath.Join(evidenceDirectory, "lifecycle.jsonl"),
		"--post-warmup-metrics", filepath.Join(evidenceDirectory, "127_0_0_1_5001-post-warmup.prom"),
		"--terminal-metrics", filepath.Join(evidenceDirectory, "terminal.prom"),
		"--storage-overlap", filepath.Join(evidenceDirectory, "storage-overlap.tsv"),
		"--storage-summary", filepath.Join(evidenceDirectory, "storage-summary.tsv"),
		"--host-io-summary", filepath.Join(evidenceDirectory, "host-io-summary.tsv"),
		"--profile-status", filepath.Join(evidenceDirectory, "threshold-pprof-status.json"),
		"--payload-root", root, "--payload-manifest", manifestPath,
		"--output", outputPath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "payload manifest") {
		t.Fatalf("missing product executable exit/stderr = %d/%q", code, stderr.String())
	}
	if _, err := os.Lstat(closurePath); !os.IsNotExist(err) {
		t.Fatalf("missing product executable published a closure: %v", err)
	}
}

func TestLocalSingleNodeStepReportRejectsProductExecutableTampering(t *testing.T) {
	for _, test := range []struct {
		name           string
		mutate         func(string) string
		resealManifest bool
	}{
		{
			name: "unsealed attestation mutation",
			mutate: func(body string) string {
				return strings.Replace(body, "post_stop_sha256\t", "post_stop_sha256\tf", 1)
			},
		},
		{
			name: "resealed cross-run invocation",
			mutate: func(body string) string {
				return strings.Replace(body,
					"baseline_invocation_id\t0123456789abcdef0123456789abcdef",
					"baseline_invocation_id\tffffffffffffffffffffffffffffffff", 1,
				)
			},
			resealManifest: true,
		},
		{
			name: "resealed post-stop binary mutation",
			mutate: func(body string) string {
				return strings.Replace(body,
					"post_stop_sha256\t"+digestLocalSingleNodeBytes([]byte("sealed-wukongim-binary")),
					"post_stop_sha256\t"+strings.Repeat("f", 64), 1,
				)
			},
			resealManifest: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			settings := localSingleNodeEvidenceFixture().Settings
			writeLocalSingleNodeCompletionStepFixture(t, root, 250, settings)
			directory := filepath.Join(root, "reports", "000250-qps")
			evidenceDirectory := filepath.Join(directory, "evidence")
			attestationPath := filepath.Join(evidenceDirectory, "product-executable.tsv")
			attestation, err := os.ReadFile(attestationPath)
			if err != nil {
				t.Fatal(err)
			}
			changed := test.mutate(string(attestation))
			if changed == string(attestation) {
				t.Fatal("fixture attestation was not changed")
			}
			if err := os.WriteFile(attestationPath, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(evidenceDirectory, "step-checksums.sha256")
			if test.resealManifest {
				writeLocalSingleNodeChecksumManifest(t, root, manifestPath, localSingleNodeManifestEntriesFixture(t, manifestPath))
			}
			code, stderr := rerunLocalSingleNodeStepFixture(t, root, 250)
			if code != exitInternal {
				t.Fatalf("tampered product executable exit/stderr = %d/%q", code, stderr)
			}
			closurePath := filepath.Join(evidenceDirectory, "step-closure.json")
			if _, err := os.Lstat(closurePath); !os.IsNotExist(err) {
				t.Fatalf("tampered product executable published a closure: %v", err)
			}
		})
	}
}

func TestLocalSingleNodeStepClosureCommandPublishesOnlyRecomputedDecision(t *testing.T) {
	root := t.TempDir()
	settings := localSingleNodeEvidenceFixture().Settings
	closure := writeLocalSingleNodeCompletionStepFixture(t, root, 250, settings)
	closurePath := filepath.Join(root, filepath.FromSlash(closure.ClosureManifest))
	decisionPath := filepath.Join(root, "reports", "000250-qps", "evidence", "typed-step-consumer.json")
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step-closure", "--root", root,
		"--closure", closurePath, "--output", decisionPath,
	}, &stderr)
	if code != 0 {
		t.Fatalf("closure consumer exit/stderr = %d/%q", code, stderr.String())
	}
	var got localbaseline.ClosedStepResult
	decodeLocalSingleNodeJSON(t, decisionPath, &got)
	if !got.Clean || got.PayloadManifestSHA256 != closure.Result.PayloadManifestSHA256 {
		t.Fatalf("consumer decision = %+v", got)
	}
}

func TestLocalSingleNodeStepReportCommandRejectsTamperedPayloadManifest(t *testing.T) {
	dir := t.TempDir()
	diagnosticPath := filepath.Join(dir, "diagnostic-summary.json")
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	planPath := filepath.Join(dir, "plan.json")
	reportPath := filepath.Join(dir, "report.json")
	lifecyclePath := filepath.Join(dir, "lifecycle.jsonl")
	baselineMetricsPath := filepath.Join(dir, "127_0_0_1_5001-post-warmup.prom")
	terminalMetricsPath := filepath.Join(dir, "terminal.prom")
	storageOverlapPath := filepath.Join(dir, "evidence", "storage-overlap.tsv")
	storageSummaryPath := filepath.Join(dir, "evidence", "storage-summary.tsv")
	hostIOSummaryPath := filepath.Join(dir, "evidence", "host-io-summary.tsv")
	profileStatusPath := filepath.Join(dir, "evidence", "threshold-pprof-status.json")
	outputPath := filepath.Join(dir, "typed-step.json")
	resultPath := filepath.Join(dir, "typed-step-result.json")
	closurePath := filepath.Join(dir, "step-closure.json")
	manifestPath := filepath.Join(dir, "step-checksums.sha256")
	settings := localSingleNodeEvidenceFixture().Settings
	step := localSingleNodeStepFixture(1000, settings)
	writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
	writeLocalSingleNodeDiagnosticFixture(t, diagnosticPath, step)
	writeLocalSingleNodeLifecycleFixture(t, lifecyclePath, step)
	baselineMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.PostWarmupCut)
	terminalMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.TerminalCut)
	if err := os.WriteFile(baselineMetricsPath, []byte(baselineMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(terminalMetricsPath, []byte(terminalMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLocalSingleNodeStorageOverlapFixture(t, storageOverlapPath, step.StorageOverlap)
	writeLocalSingleNodeSummaryFixtures(t, storageSummaryPath, hostIOSummaryPath, 1000)
	writeLocalSingleNodeNotTriggeredProfileFixture(t, profileStatusPath)
	manifestEntries := []string{
		"scenario.yaml", "plan.json", "report.json", "diagnostic-summary.json", "lifecycle.jsonl", "127_0_0_1_5001-post-warmup.prom", "terminal.prom",
		"evidence/storage-overlap.tsv", "evidence/storage-summary.tsv", "evidence/host-io-summary.tsv",
		"evidence/threshold-pprof-status.json",
	}
	manifestEntries = append(manifestEntries, localSingleNodeStorageManifestPaths(step.StorageOverlap)...)
	manifestEntries = append(manifestEntries, ensureLocalSingleNodeExecutionPayloadFixture(t, dir, 1000)...)
	writeLocalSingleNodeChecksumManifest(t, dir, manifestPath, manifestEntries)
	if err := os.WriteFile(terminalMetricsPath, []byte(terminalMetrics+"# tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-step",
		"--offered-qps", "1000", "--required-active-connections", "2500",
		"--group-members", "10",
		"--warmup-seconds", "60", "--measured-seconds", "300", "--drain-budget-seconds", "90",
		"--maximum-sample-gap-seconds", "30", "--scenario", scenarioPath, "--plan", planPath, "--run-report", reportPath, "--diagnostic-summary", diagnosticPath,
		"--lifecycle", lifecyclePath, "--post-warmup-metrics", baselineMetricsPath,
		"--terminal-metrics", terminalMetricsPath, "--storage-overlap", storageOverlapPath,
		"--storage-summary", storageSummaryPath, "--host-io-summary", hostIOSummaryPath,
		"--profile-status", profileStatusPath,
		"--payload-root", dir, "--payload-manifest", manifestPath,
		"--output", outputPath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)

	if code != exitInternal {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitInternal, stderr.String())
	}
	var decision localbaseline.ClosedStepResult
	decodeLocalSingleNodeJSON(t, resultPath, &decision)
	if decision.Clean || decision.Outcome != localbaseline.OutcomeInsufficientEvidence {
		t.Fatalf("tampered closed step result = %+v", decision)
	}
}

func TestLocalSingleNodeStepReportCommandRejectsInputOutsideManifest(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	diagnosticPath := filepath.Join(outside, "diagnostic-summary.json")
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	planPath := filepath.Join(dir, "plan.json")
	reportPath := filepath.Join(dir, "report.json")
	lifecyclePath := filepath.Join(dir, "lifecycle.jsonl")
	baselineMetricsPath := filepath.Join(dir, "127_0_0_1_5001-post-warmup.prom")
	terminalMetricsPath := filepath.Join(dir, "terminal.prom")
	storageOverlapPath := filepath.Join(dir, "evidence", "storage-overlap.tsv")
	storageSummaryPath := filepath.Join(dir, "evidence", "storage-summary.tsv")
	hostIOSummaryPath := filepath.Join(dir, "evidence", "host-io-summary.tsv")
	profileStatusPath := filepath.Join(dir, "evidence", "threshold-pprof-status.json")
	outputPath := filepath.Join(dir, "typed-step.json")
	resultPath := filepath.Join(dir, "typed-step-result.json")
	closurePath := filepath.Join(dir, "step-closure.json")
	manifestPath := filepath.Join(dir, "step-checksums.sha256")
	settings := localSingleNodeEvidenceFixture().Settings
	step := localSingleNodeStepFixture(1000, settings)
	writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
	writeLocalSingleNodeDiagnosticFixture(t, diagnosticPath, step)
	writeLocalSingleNodeLifecycleFixture(t, lifecyclePath, step)
	baselineMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.PostWarmupCut)
	terminalMetrics := localSingleNodeProductQueueMetrics(step.ProductQueues.TerminalCut)
	if err := os.WriteFile(baselineMetricsPath, []byte(baselineMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(terminalMetricsPath, []byte(terminalMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLocalSingleNodeStorageOverlapFixture(t, storageOverlapPath, step.StorageOverlap)
	writeLocalSingleNodeSummaryFixtures(t, storageSummaryPath, hostIOSummaryPath, 1000)
	writeLocalSingleNodeNotTriggeredProfileFixture(t, profileStatusPath)
	manifestEntries := []string{
		"scenario.yaml", "plan.json", "report.json", "lifecycle.jsonl", "127_0_0_1_5001-post-warmup.prom", "terminal.prom", "evidence/storage-overlap.tsv",
		"evidence/storage-summary.tsv", "evidence/host-io-summary.tsv",
		"evidence/threshold-pprof-status.json",
	}
	manifestEntries = append(manifestEntries, localSingleNodeStorageManifestPaths(step.StorageOverlap)...)
	manifestEntries = append(manifestEntries, ensureLocalSingleNodeExecutionPayloadFixture(t, dir, 1000)...)
	writeLocalSingleNodeChecksumManifest(t, dir, manifestPath, manifestEntries)
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step", "--offered-qps", "1000",
		"--required-active-connections", "2500", "--group-members", "10", "--warmup-seconds", "60",
		"--measured-seconds", "300", "--drain-budget-seconds", "90",
		"--maximum-sample-gap-seconds", "30", "--scenario", scenarioPath, "--plan", planPath, "--run-report", reportPath, "--diagnostic-summary", diagnosticPath,
		"--lifecycle", lifecyclePath, "--post-warmup-metrics", baselineMetricsPath,
		"--terminal-metrics", terminalMetricsPath, "--storage-overlap", storageOverlapPath,
		"--storage-summary", storageSummaryPath, "--host-io-summary", hostIOSummaryPath,
		"--profile-status", profileStatusPath,
		"--payload-root", dir, "--payload-manifest", manifestPath,
		"--output", outputPath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)
	if code != exitInternal {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitInternal, stderr.String())
	}
	var decision localbaseline.ClosedStepResult
	decodeLocalSingleNodeJSON(t, resultPath, &decision)
	if decision.Clean || decision.Outcome != localbaseline.OutcomeInsufficientEvidence {
		t.Fatalf("outside-manifest closed step result = %+v", decision)
	}
}

func TestLocalSingleNodeProfilePayloadRequiresEveryCompleteBlobInManifest(t *testing.T) {
	dir := t.TempDir()
	evidenceDir := filepath.Join(dir, "evidence")
	profileDir := filepath.Join(evidenceDir, "threshold-pprof")
	blobsDir := filepath.Join(profileDir, "profiles")
	if err := os.MkdirAll(blobsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	current := previous.Add(time.Second)
	trigger := localbaseline.ProfileThresholdTrigger{
		Kind: localbaseline.ProfileTriggerActualOfferedRatio, PreviousAt: previous, CurrentAt: current,
		AcknowledgedDelta: 80, IntervalSeconds: 1, ExpectedOffered: 100, ActualOfferedPercent: 80,
	}
	metadata := map[string]any{
		"schema": "wukongim.local_threshold_pprof/v1",
		"trigger": map[string]any{
			"kind": trigger.Kind, "observed_phase": "measurement",
			"previous_utc": previous, "current_utc": current,
		},
		"capture": map[string]any{
			"status": "complete", "valid": true, "reason": "ok",
			"start_phase": "measurement", "end_phase": "measurement",
			"started_at_utc": current, "completed_at_utc": current.Add(time.Second), "cpu_seconds": 1,
		},
		"nodes": []map[string]any{{
			"node": "node-1", "cpu": "complete", "heap": "complete", "goroutine": "complete",
		}},
	}
	writeAnyLocalSingleNodeJSONFixture(t, filepath.Join(profileDir, "metadata.json"), metadata)
	for _, name := range []string{"node-1-cpu.pb.gz", "node-1-heap.pb.gz", "node-1-goroutine.txt"} {
		if err := os.WriteFile(filepath.Join(blobsDir, name), []byte("bounded-profile"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	helperExit := 0
	statusPath := filepath.Join(evidenceDir, "threshold-pprof-status.json")
	writeAnyLocalSingleNodeJSONFixture(t, statusPath, localbaseline.ProfileEvidence{
		Schema: localbaseline.ProfileEvidenceSchema, Status: "complete", EvidenceComplete: true,
		CaptureValid: true, Reason: "ok", Triggered: true, Trigger: &trigger,
		Metadata: "threshold-pprof/metadata.json", HelperExitStatus: &helperExit,
	})
	manifestPath := filepath.Join(evidenceDir, "step-checksums.sha256")
	writeLocalSingleNodeChecksumManifest(t, dir, manifestPath, []string{
		"evidence/threshold-pprof-status.json", "evidence/threshold-pprof/metadata.json",
		"evidence/threshold-pprof/profiles/node-1-cpu.pb.gz",
		"evidence/threshold-pprof/profiles/node-1-heap.pb.gz",
	})
	manifest, err := verifyLocalSingleNodeChecksumManifest(dir, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := localbaseline.ReadSingleNodeProfileEvidence(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var missing bool
	for _, path := range localSingleNodeProfilePayloadPaths(statusPath, evidence) {
		if err := manifest.requireInput(path); err != nil {
			missing = true
			break
		}
	}
	if !missing {
		t.Fatal("complete goroutine profile absent from manifest was accepted")
	}
}

func TestLocalSingleNodeBaselineReportCommandWritesClosedGate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(dir, "evidence-draft.json")
	sealedEvidencePath := filepath.Join(dir, "reports", "local-baseline-evidence.json")
	resultPath := filepath.Join(dir, "authorization.json")
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = nil
	evidence.CompletionGeneration = ""
	writeLocalSingleNodeEvidenceFixture(t, evidencePath, evidence)
	closures := writeLocalSingleNodeBaselineClosureFixtures(t, dir, evidence.Settings, 1000)
	var stderr bytes.Buffer

	args := []string{
		"report", "local-single-node-baseline",
		"--root", dir, "--evidence", evidencePath, "--sealed-evidence-output", sealedEvidencePath,
		"--output", resultPath,
	}
	for _, closure := range closures {
		args = append(args, "--step-closure", closure)
	}
	code := runWithStderr(args, &stderr)

	if code != exitHardLimit {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitHardLimit, stderr.String())
	}
	var result localbaseline.AuthorizationResult
	decodeLocalSingleNodeJSON(t, resultPath, &result)
	if result.Authorizes || result.ReviewedContractSatisfied {
		t.Fatalf("authorization = %+v, want closed", result)
	}
}

func TestLocalSingleNodeBaselineReportCommandReturnsInsufficientEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(dir, "evidence-draft.json")
	sealedEvidencePath := filepath.Join(dir, "reports", "local-baseline-evidence.json")
	resultPath := filepath.Join(dir, "authorization.json")
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = nil
	evidence.CompletionGeneration = ""
	writeLocalSingleNodeEvidenceFixture(t, evidencePath, evidence)
	closures := writeLocalSingleNodeBaselineClosureFixtures(t, dir, evidence.Settings, -1)
	closures = closures[:3]
	var stderr bytes.Buffer

	args := []string{
		"report", "local-single-node-baseline",
		"--root", dir, "--evidence", evidencePath, "--sealed-evidence-output", sealedEvidencePath,
		"--output", resultPath,
	}
	for _, closure := range closures {
		args = append(args, "--step-closure", closure)
	}
	code := runWithStderr(args, &stderr)

	if code != exitInternal {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitInternal, stderr.String())
	}
	var result localbaseline.AuthorizationResult
	decodeLocalSingleNodeJSON(t, resultPath, &result)
	if result.Authorizes || result.Outcome != localbaseline.OutcomeInsufficientEvidence || len(result.Steps) != 3 {
		t.Fatalf("authorization = %+v, want insufficient evidence", result)
	}
}

func TestLocalSingleNodeBaselineReportCommandRejectsMalformedEvidence(t *testing.T) {
	dir := t.TempDir()
	evidencePath := filepath.Join(dir, "evidence.json")
	resultPath := filepath.Join(dir, "authorization.json")
	if err := os.WriteFile(evidencePath, []byte(`{"schema":"unknown","extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-baseline",
		"--root", dir, "--evidence", evidencePath,
		"--sealed-evidence-output", filepath.Join(dir, "sealed.json"),
		"--output", resultPath,
	}, &stderr)

	if code != exitConfig || !strings.Contains(stderr.String(), "evidence parse failed") {
		t.Fatalf("exit/stderr = %d/%q, want config parse failure", code, stderr.String())
	}
	if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
		t.Fatalf("malformed evidence created output: %v", err)
	}
}

func writeLocalSingleNodeBaselineClosureFixtures(
	t *testing.T,
	root string,
	settings localbaseline.ReviewedSettings,
	failingQPS int,
) []string {
	t.Helper()
	paths := make([]string, 0, len(localbaseline.ReviewedOfferedSendQPS))
	for _, qps := range localbaseline.ReviewedOfferedSendQPS {
		if qps == failingQPS {
			writeLocalSingleNodeCompletionStepFixtureWithMutation(t, root, qps, settings, func(step *localbaseline.StepEvidence) {
				for phaseIndex := range []int{0, 1, 2} {
					var phase *localbaseline.PhaseEvidence
					switch phaseIndex {
					case 0:
						phase = &step.Timeline.Warmup
					case 1:
						phase = &step.Timeline.Measured
					default:
						phase = &step.Timeline.Drain
					}
					for index := range phase.Samples {
						phase.Samples[index].ActiveConnections--
					}
				}
			}, exitHardLimit)
		} else {
			writeLocalSingleNodeCompletionStepFixture(t, root, qps, settings)
		}
		paths = append(paths, filepath.Join(root, "reports", fmt.Sprintf("%06d-qps", qps), "evidence", "step-closure.json"))
	}
	return paths
}

func localSingleNodeEvidenceFixture() localbaseline.BaselineEvidence {
	settings := localbaseline.ReviewedSettings{
		Channels:          1000,
		ActiveConnections: 2500, WarmupSeconds: 60, MeasuredSeconds: 300, DrainBudgetSeconds: 90,
		GroupMembers: 10, SendConcurrency: 2800, PayloadBytes: 128, ACKTimeoutSeconds: 15,
		ReceiveACK: true, HeartbeatEnabled: true, SenderPickRoundRobin: true, MinimumFreePercent: 10,
		LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 1, ChannelReplicas: 1,
		CommitFlushWindowMicros: 200, CommitCoordinatorShards: 1, SyncCommit: true, CleanCluster: true,
		OwnedCluster: true, OwnedWorker: true, CanonicalSourceConfig: true, MetricsEndpointCount: 1,
	}
	closures := make([]localbaseline.StepClosure, 0, len(localbaseline.ReviewedOfferedSendQPS))
	for index, qps := range localbaseline.ReviewedOfferedSendQPS {
		evidence := localSingleNodeStepFixture(qps, settings)
		payloadDigest := strings.Repeat(string(rune('a'+index)), 64)
		closures = append(closures, localbaseline.StepClosure{
			Schema:                localbaseline.StepClosureSchema,
			ClosureManifest:       fmt.Sprintf("reports/%04d-qps/evidence/step-closure.json", qps),
			ClosureManifestSHA256: strings.Repeat(string(rune('e'-index)), 64),
			Evidence:              evidence, Result: localbaseline.CloseStepResult(evidence, payloadDigest),
		})
	}
	evidence := localbaseline.BaselineEvidence{
		Schema:                        localbaseline.BaselineEvidenceSchema,
		BaselineInvocationID:          "0123456789abcdef0123456789abcdef",
		DiagnosticOutcome:             string(localbaseline.OutcomeClean),
		FilesystemObservationComplete: true,
		ObservedFilesystemFreePercent: 50,
		CanonicalDataDir:              "/var/lib/wukongim",
		DataFilesystemDevice:          "2049",
		DataFilesystemTotalBlocks:     100000,
		DataFilesystemBlockSize:       4096,
		Settings:                      settings,
		Source: localbaseline.SourceEvidence{
			Revision: strings.Repeat("a", 40), Dirty: false, RebuildableFromRevision: true,
		},
		Seal:         localbaseline.SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
		StepClosures: closures,
	}
	localbaseline.SealBaselineEvidence(&evidence)
	return evidence
}

func localSingleNodeTestRunID(qps int) string {
	return fmt.Sprintf("single-node-0123456789abcdef0123456789abcdef-fixed-1000ch-%06d-qps", qps)
}

func localSingleNodeRecloseFixture(evidence *localbaseline.BaselineEvidence, index int) {
	closure := &evidence.StepClosures[index]
	closure.Result = localbaseline.CloseStepResult(closure.Evidence, closure.Result.PayloadManifestSHA256)
}

func localSingleNodeStepFixture(qps int, settings localbaseline.ReviewedSettings) localbaseline.StepEvidence {
	runID := localSingleNodeTestRunID(qps)
	start := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC).Add(time.Duration(qps) * time.Minute)
	warmup := localSingleNodePhaseFixture(start, time.Minute, 2500)
	measured := localSingleNodePhaseFixture(warmup.EndedAt, 5*time.Minute, 2500)
	drain := localSingleNodePhaseFixture(measured.EndedAt, 30*time.Second, 2500)
	server := localbaseline.ProcessEvidence{PID: 1000 + qps, StartToken: fmt.Sprintf("server-%d", qps), Alive: true}
	for _, phase := range []*localbaseline.PhaseEvidence{&warmup, &measured, &drain} {
		for index := range phase.Samples {
			phase.Samples[index].Server = server
		}
	}
	planned := uint64(qps * settings.MeasuredSeconds)
	traffic := localbaseline.TrafficEvidence{
		WarmupSendACKs: 1000,
		Planned:        planned, Dispatched: planned, LogicalSent: planned, SendAttempts: planned,
		SendACKs: planned, StableClientMsgNo: true, RetryEvidenceComplete: true,
		MaximumRetriesPerMessage: 3,
	}
	setLocalSingleNodePhaseTraffic(&measured, traffic, true)
	setLocalSingleNodePhaseTraffic(&drain, traffic, false)
	cutAt := drain.StartedAt.Add(3 * time.Second)
	terminalCut := localbaseline.ProductQueueCut{
		Schema: localbaseline.ProductQueueCutSchema, ObservedAt: cutAt,
		RunID: runID, AssignmentID: "assignment-1", Phase: "run", ActivePhase: "cooldown",
	}
	storage := localSingleNodeStorageFixture(measured, drain, runID)
	storageDigest := digestLocalSingleNodeBytes([]byte(localSingleNodeStorageOverlapBody(storage)))
	storage.PayloadSHA256 = storageDigest
	binding := &localbaseline.TerminalCutBinding{
		RunID: runID, AssignmentID: "assignment-1",
		ReadyAt: drain.StartedAt.Add(2 * time.Second), DeadlineAt: drain.StartedAt.Add(90 * time.Second),
		ObservedAt: cutAt, AcknowledgedAt: drain.StartedAt.Add(4 * time.Second),
		StorageOverlapSHA256: storageDigest,
	}
	terminalReceiveDrain := localSingleNodeReceiveDrainFixture(settings.ActiveConnections)
	logicalACKs := traffic.WarmupSendACKs + traffic.SendACKs
	expectedReceives := logicalACKs * uint64(settings.GroupMembers-1)
	terminalReceiveDrain.ReceiveFramesObserved = expectedReceives
	terminalReceiveDrain.RecvACKSuccesses = expectedReceives
	terminalReceiveDrain.FanoutProof = localSingleNodeFanoutProofFixture(logicalACKs, expectedReceives)
	binding.ReceiveDrainSHA256 = benchmodel.ReceiveDrainFingerprint(terminalReceiveDrain)
	terminalCut.ReceiveDrainSHA256 = binding.ReceiveDrainSHA256
	binding.ProductMetricsSHA256 = digestLocalSingleNodeBytes([]byte(localSingleNodeProductQueueMetrics(terminalCut)))
	storageMetrics, err := localbaseline.ParseStorageMetricsSummary(
		strings.NewReader(localSingleNodeStorageSummaryFixture(qps)), fmt.Sprintf("%06d", qps), "127_0_0_1_5001",
	)
	if err != nil {
		panic(err)
	}
	hostIO, err := localbaseline.ParseHostIOSummary(
		strings.NewReader(localSingleNodeHostIOSummaryFixture(qps)), fmt.Sprintf("%06d", qps), "host-local",
	)
	if err != nil {
		panic(err)
	}
	return localbaseline.StepEvidence{
		Schema: localbaseline.StepEvidenceSchema, RunID: runID, AssignmentID: "assignment-1", OfferedSendQPS: qps,
		RequiredActiveConnections: settings.ActiveConnections,
		ConfiguredGroupMembers:    settings.GroupMembers,
		ConfiguredWarmupSeconds:   settings.WarmupSeconds, ConfiguredMeasuredSeconds: settings.MeasuredSeconds,
		ConfiguredDrainBudgetSeconds: settings.DrainBudgetSeconds, MaximumSampleGapSeconds: 30,
		Target: localbaseline.ReviewedTargetEvidence{
			APIAddress: "http://127.0.0.1:5001", GatewayAddress: "127.0.0.1:5100",
			MetricsAddress: "http://127.0.0.1:5001", WorkerAddress: "http://127.0.0.1:19130",
		},
		ExecutionSeal: localbaseline.ExecutionSealEvidence{
			BaselineInvocationID:  "0123456789abcdef0123456789abcdef",
			SourceConfigSHA256:    strings.Repeat("d", 64),
			EffectiveConfigSHA256: strings.Repeat("1", 64), WukongIMBinarySHA256: strings.Repeat("2", 64),
			WkbenchBinarySHA256: strings.Repeat("3", 64),
		},
		Timeline: localbaseline.TimelineEvidence{
			CaptureComplete: true, Warmup: warmup, Measured: measured, Drain: drain,
			Terminal: localbaseline.RuntimeSample{
				ObservedAt:          drain.EndedAt.Add(time.Second),
				ActiveConnections:   settings.ActiveConnections,
				TerminalPreClose:    true,
				TerminalCutRequired: true,
				TerminalCutReady:    true,
				TerminalCut:         binding,
				Server:              server,
				Worker:              localbaseline.ProcessEvidence{PID: 202, StartToken: "worker", Alive: true},
				Traffic:             traffic,
				ReceiveDrain:        terminalReceiveDrain,
			},
		},
		Traffic: traffic,
		ProductQueues: localbaseline.ProductQueueEvidence{
			BoundaryEvidenceComplete: true,
			PostWarmupCut: localbaseline.ProductQueueCut{
				Schema: localbaseline.ProductQueueCutSchema, ObservedAt: measured.StartedAt.Add(time.Second),
				RunID: runID, AssignmentID: "assignment-1", Phase: "warmup", ActivePhase: "run",
				ReceiveDrainSHA256: benchmodel.ReceiveDrainFingerprint(measured.Samples[0].ReceiveDrain),
			},
			TerminalCut:           terminalCut,
			TerminalPayloadSHA256: binding.ProductMetricsSHA256,
			Queues: []localbaseline.ProductQueueBoundary{
				{Name: localbaseline.QueueGatewayAsyncSend}, {Name: localbaseline.QueueChannelMailbox},
				{Name: localbaseline.QueueChannelWorker}, {Name: localbaseline.QueueRuntimePool},
				{Name: localbaseline.QueueChannelAppendPending}, {Name: localbaseline.QueueChannelAppendInflight},
				{Name: localbaseline.QueuePostCommitBacklog}, {Name: localbaseline.QueuePostCommitHandoff},
				{Name: localbaseline.QueuePostCommitRetry}, {Name: localbaseline.QueueEffectPoolInflight},
				{Name: localbaseline.QueueStorageCommit},
			},
		},
		StorageOverlap: storage, StorageMetrics: storageMetrics, HostIO: hostIO,
		Profile: localbaseline.ProfileEvidence{
			Schema: localbaseline.ProfileEvidenceSchema, Status: "not_triggered", EvidenceComplete: true,
			CaptureValid: true, Reason: "no_measured_threshold",
		},
		Seal: localbaseline.SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
	}
}

func writeLocalSingleNodeReviewedExecutionFixture(t *testing.T, scenarioPath, planPath, reportPath string, step localbaseline.StepEvidence, settings localbaseline.ReviewedSettings) {
	t.Helper()
	profileName := "thousand-groups"
	workerID := "worker-a"
	ratePerChannel := float64(step.OfferedSendQPS) / 1000
	scenario := benchmodel.Scenario{
		Version: "wkbench/v1",
		Run: benchmodel.RunConfig{
			ID: step.RunID, Duration: time.Duration(settings.MeasuredSeconds) * time.Second,
			Warmup: time.Duration(settings.WarmupSeconds) * time.Second, Cooldown: time.Duration(settings.DrainBudgetSeconds) * time.Second,
			ExternalTerminalCut: true, FailFast: true,
		},
		Objectives: benchmodel.ObjectivesConfig{
			Scale: "small", IngressQPS: benchmodel.Rate{PerSecond: float64(step.OfferedSendQPS)},
			OnlineFanoutQPS: benchmodel.Rate{PerSecond: float64(step.OfferedSendQPS * (settings.GroupMembers - 1))},
			ToleranceRatio:  0.1,
		},
		Limits: benchmodel.LimitsConfig{Hard: benchmodel.HardLimitsConfig{}},
		Identity: benchmodel.IdentityConfig{
			UIDPrefix: "bench-u", DevicePrefix: "bench-d", ClientMsgPrefix: "bench-msg",
			Token: benchmodel.TokenConfig{Mode: "bench_api"},
		},
		Online: benchmodel.OnlineConfig{
			TotalUsers: settings.ActiveConnections, GatewayBalance: "round_robin",
			Heartbeat: benchmodel.HeartbeatConfig{Enabled: true, Interval: 30 * time.Second, Timeout: 5 * time.Second},
		},
		Channels: benchmodel.ChannelsConfig{Profiles: []benchmodel.ChannelProfile{{
			Name: profileName, ChannelType: benchmodel.ChannelTypeGroup, Count: 1000,
			Members: benchmodel.MembersConfig{Count: settings.GroupMembers, Overlap: "allowed"},
			Online:  benchmodel.ChannelOnlineConfig{MemberRatio: 1},
			Shard:   benchmodel.ShardConfig{Mode: "hash"},
			Prepare: benchmodel.ChannelPrepareConfig{SubscribersBatchSize: 1000},
		}}},
		Messages: benchmodel.MessagesConfig{
			Payload: benchmodel.PayloadConfig{SizeBytes: 128, Mode: "deterministic"},
			Traffic: []benchmodel.TrafficConfig{{
				Name: "group-send", ChannelRef: profileName, RatePerChannel: benchmodel.Rate{PerSecond: ratePerChannel},
				Concurrency: 2800, AckTimeout: 15 * time.Second, Retry: benchmodel.TrafficRetryConfig{Enabled: true},
				SenderPick: "round_robin", RecvAck: true, Verify: benchmodel.VerifyConfig{Recv: benchmodel.RecvVerifyConfig{Mode: "none"}},
			}},
		},
	}
	owners := make(map[int]string, 1000)
	for channel := 0; channel < 1000; channel++ {
		owners[channel] = workerID
	}
	identityRange := benchmodel.Range{Start: 0, End: settings.ActiveConnections}
	plan := benchmodel.Plan{
		RunID: step.RunID, WorkerOrder: []string{workerID}, ProfileOrder: []string{profileName},
		IdentityPool: identityRange, OnlineIdentityPool: identityRange,
		ChannelOwners: map[string]map[int]string{profileName: owners},
		Workers: map[string]benchmodel.WorkerPlan{workerID: {
			WorkerID: workerID, IdentityRange: identityRange,
			Profiles: map[string]benchmodel.ProfileShard{profileName: {
				Name: profileName, ChannelType: benchmodel.ChannelTypeGroup,
				ChannelRange: benchmodel.Range{Start: 0, End: 1000}, MemberRange: identityRange,
				MemberReusePolicy: "allowed", GlobalRate: benchmodel.Rate{PerSecond: ratePerChannel},
				LocalRate: benchmodel.Rate{PerSecond: ratePerChannel},
			}},
		}},
	}
	report := benchreport.Report{
		RunID: step.RunID, Status: benchreport.StatusPassed, ExitCode: benchreport.ExitSuccess,
		StabilityVerdict: benchreport.VerdictInsufficientEvidence,
		Scenario:         scenario,
		Target: benchmodel.Target{
			Name: "local-single-node-cluster",
			API:  benchmodel.TargetAPIConfig{Addrs: []string{"http://127.0.0.1:5001"}},
			Gateway: benchmodel.TargetGatewayConfig{TCP: benchmodel.TargetGatewayTCPConfig{
				Addrs: []string{"127.0.0.1:5100"},
			}},
			BenchAPI: benchmodel.BenchAPIConfig{Enabled: true, Addrs: []string{"http://127.0.0.1:5001"}},
			Metrics:  benchmodel.MetricsConfig{Enabled: true, Addrs: []string{"http://127.0.0.1:5001"}},
		},
		Workers: benchmodel.WorkerSet{Workers: []benchmodel.Worker{{
			ID: workerID, Addr: "http://127.0.0.1:19130", Weight: 1, InsecureControl: true,
		}}},
		Plan: plan,
	}
	writeLocalSingleNodeCanonicalExecutionFixture(t, scenarioPath, planPath, reportPath, report)
}

func writeLocalSingleNodeCanonicalExecutionFixture(t *testing.T, scenarioPath, planPath, reportPath string, report benchreport.Report) {
	t.Helper()
	scenarioData, err := yaml.Marshal(report.Scenario)
	if err != nil {
		t.Fatal(err)
	}
	planData, err := json.MarshalIndent(report.Plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	reportData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		scenarioPath: scenarioData,
		planPath:     append(planData, '\n'),
		reportPath:   append(reportData, '\n'),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func readLocalSingleNodeTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeLocalSingleNodeSummaryFixtures(t *testing.T, storagePath, hostPath string, qps int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storagePath, []byte(localSingleNodeStorageSummaryFixture(qps)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostPath, []byte(localSingleNodeHostIOSummaryFixture(qps)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func localSingleNodeStorageSummaryFixture(qps int) string {
	planned := uint64(qps * 300)
	physical := uint64(100)
	fields := []string{
		fmt.Sprintf("%06d", qps), "127_0_0_1_5001", "complete", "3", fmt.Sprint(physical),
		fmt.Sprint(planned), fmt.Sprint(planned), fmt.Sprint(planned * 2048),
		fmt.Sprintf("%.6f", float64(planned)/float64(physical)), fmt.Sprintf("%.6f", float64(planned)/float64(physical)),
		"0.100000", "0.200000", "0.300000", "0.400000", "1.000000",
		fmt.Sprint(planned), "0.500000", fmt.Sprint(planned), "0.500000",
		"0", "0.000000", "0", "0.000000", "0", "0.000000",
		fmt.Sprint(planned), "0.400000", "0", "0.000000", "0", "0.000000",
		fmt.Sprint(planned * 2048), fmt.Sprint(planned * 2048), "1.000000",
		"2048", "2", "4096", "8192", "1", "1048576", "0", "1", "2", "1073741824",
		fmt.Sprintf("%.6f", float64(planned*2048)/float64(physical)),
		"1000.000000", "3000.000000", "5000.000000", "1000.000000", "3000.000000", "5000.000000",
		"2048000.000000", "6144000.000000", "10240000.000000",
	}
	return strings.Join(localStepStorageHeader, "\t") + "\n" + strings.Join(fields, "\t") + "\n"
}

func localSingleNodeHostIOSummaryFixture(qps int) string {
	fields := []string{
		fmt.Sprintf("%06d", qps), "host-local", "complete", "nvme0n1", "1", "1200.000000",
		"1", "80000000.000000", "1", "72.000000", "1", "1.250000", "1",
	}
	return strings.Join(localStepHostIOHeader, "\t") + "\n" + strings.Join(fields, "\t") + "\n"
}

func localSingleNodeStorageFixture(measured, drain localbaseline.PhaseEvidence, runID string) localbaseline.StorageOverlapEvidence {
	identity := fmt.Sprintf("%x", sha256.Sum256(nil))
	samples := make([]localbaseline.StorageOverlapSample, 0, 14)
	for observedAt, index := measured.StartedAt.Add(time.Second), 0; observedAt.Before(measured.EndedAt); observedAt, index = observedAt.Add(25*time.Second), index+1 {
		name := "post-warmup"
		if index > 0 {
			name = fmt.Sprintf("periodic-%06d", index)
		}
		samples = append(samples, localbaseline.StorageOverlapSample{
			ObservedAt: observedAt, RunID: runID, Sample: name, Node: "node-1", Status: "complete",
			CompactionCount: 10, SnapshotIdentity: identity,
			SnapshotInventory: "snapshot-inventory/" + name + "-node-1.tsv", InventoryVerified: true,
		})
	}
	samples = append(samples, localbaseline.StorageOverlapSample{
		ObservedAt: drain.StartedAt.Add(3 * time.Second), RunID: runID, Sample: "terminal", Node: "node-1", Status: "complete",
		CompactionCount: 10, SnapshotIdentity: identity,
		SnapshotInventory: "snapshot-inventory/terminal-node-1.tsv", InventoryVerified: true,
	})
	return localbaseline.StorageOverlapEvidence{CaptureComplete: true, Samples: samples}
}

func writeLocalSingleNodeStorageOverlapFixture(t *testing.T, path string, evidence localbaseline.StorageOverlapEvidence) {
	t.Helper()
	inventoryDirectory := filepath.Join(filepath.Dir(path), "snapshot-inventory")
	if err := os.MkdirAll(inventoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, sample := range evidence.Samples {
		if err := os.WriteFile(filepath.Join(inventoryDirectory, sample.Sample+"-node-1.tsv"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(localSingleNodeStorageOverlapBody(evidence)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func localSingleNodeStorageOverlapBody(evidence localbaseline.StorageOverlapEvidence) string {
	rows := []string{"observed_at_utc\trun_id\tsample\tnode\tstatus\tcompaction_count\tcompactions_in_progress\tsnapshot_files\tsnapshot_bytes\tsnapshot_identity\tsnapshot_inventory"}
	for _, sample := range evidence.Samples {
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s\tnode-1\tcomplete\t%d\t%d\t0\t0\t%s\tsnapshot-inventory/%s-node-1.tsv",
			sample.ObservedAt.Format(time.RFC3339Nano), sample.RunID, sample.Sample, sample.CompactionCount,
			sample.CompactionsInProgress, sample.SnapshotIdentity, sample.Sample))
	}
	return strings.Join(rows, "\n") + "\n"
}

func localSingleNodeStorageManifestPaths(evidence localbaseline.StorageOverlapEvidence) []string {
	paths := make([]string, 0, len(evidence.Samples))
	for _, sample := range evidence.Samples {
		paths = append(paths, "evidence/"+sample.SnapshotInventory)
	}
	return paths
}

func writeLocalSingleNodeNotTriggeredProfileFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(localbaseline.ProfileEvidence{
		Schema: localbaseline.ProfileEvidenceSchema, Status: "not_triggered", EvidenceComplete: true,
		CaptureValid: true, Reason: "no_measured_threshold",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func localSingleNodePhaseFixture(start time.Time, duration time.Duration, active int) localbaseline.PhaseEvidence {
	end := start.Add(duration)
	samples := make([]localbaseline.RuntimeSample, 0, int(duration/(30*time.Second))+1)
	for observedAt := start; !observedAt.After(end); observedAt = observedAt.Add(30 * time.Second) {
		samples = append(samples, localbaseline.RuntimeSample{
			ObservedAt: observedAt, ActiveConnections: active,
			Server:       localbaseline.ProcessEvidence{PID: 101, StartToken: "server", Alive: true},
			Worker:       localbaseline.ProcessEvidence{PID: 202, StartToken: "worker", Alive: true},
			ReceiveDrain: localSingleNodeReceiveDrainFixture(active),
		})
	}
	return localbaseline.PhaseEvidence{StartedAt: start, EndedAt: end, Samples: samples}
}

func localSingleNodeReceiveDrainFixture(clients int) benchmodel.ReceiveDrainSnapshot {
	return benchmodel.ReceiveDrainSnapshot{
		Required: clients > 0, EvidenceComplete: true, DrainComplete: true,
		ClientCount: uint64(clients), ActiveDrains: uint64(clients), QueueSnapshotClients: uint64(clients),
		StableZeroObservations: benchmodel.ReceiveDrainStableZeroObservations,
		FanoutProof:            localSingleNodeFanoutProofFixture(0, 0),
	}
}

func localSingleNodeFanoutProofFixture(logical, recipients uint64) benchmodel.FanoutProofSnapshot {
	summary := benchmodel.FanoutMultisetSummary{
		Count: recipients, DigestA: strings.Repeat("a", 64), DigestB: strings.Repeat("b", 64),
	}
	if recipients == 0 {
		summary.DigestA = strings.Repeat("0", 64)
		summary.DigestB = strings.Repeat("0", 64)
	}
	return benchmodel.FanoutProofSnapshot{
		Version: benchmodel.FanoutProofVersion, Required: true, EvidenceComplete: true,
		LogicalSendACKs: logical, Expected: summary, Received: summary, RecvACKed: summary,
	}
}

func setLocalSingleNodePhaseTraffic(phase *localbaseline.PhaseEvidence, final localbaseline.TrafficEvidence, progressive bool) {
	if phase == nil || len(phase.Samples) == 0 {
		return
	}
	for index := range phase.Samples {
		traffic := final
		if progressive {
			numerator := uint64(index + 1)
			denominator := uint64(len(phase.Samples))
			traffic.Planned = final.Planned * numerator / denominator
			traffic.Dispatched = final.Dispatched * numerator / denominator
			traffic.LogicalSent = final.LogicalSent * numerator / denominator
			traffic.SendACKs = final.SendACKs * numerator / denominator
			traffic.TerminalErrors = final.TerminalErrors * numerator / denominator
			traffic.CorrectnessErrors = final.CorrectnessErrors * numerator / denominator
			traffic.RetryAttempts = final.RetryAttempts * numerator / denominator
			traffic.RetryExhausted = final.RetryExhausted * numerator / denominator
			traffic.SendAttempts = final.SendAttempts * numerator / denominator
			traffic.Remaining = 0
		}
		phase.Samples[index].Traffic = traffic
	}
}

func setLocalSingleNodeStepTraffic(step *localbaseline.StepEvidence, traffic localbaseline.TrafficEvidence) {
	step.Traffic = traffic
	setLocalSingleNodePhaseTraffic(&step.Timeline.Measured, traffic, true)
	setLocalSingleNodePhaseTraffic(&step.Timeline.Drain, traffic, false)
	step.Timeline.Terminal.Traffic = traffic
}

func writeLocalSingleNodeEvidenceFixture(t *testing.T, path string, evidence localbaseline.BaselineEvidence) {
	t.Helper()
	writeAnyLocalSingleNodeJSONFixture(t, path, evidence)
}

func writeAnyLocalSingleNodeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeLocalSingleNodeJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func writeLocalSingleNodeChecksumManifest(t *testing.T, root, manifest string, relatives []string) {
	t.Helper()
	var lines strings.Builder
	for _, relative := range relatives {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&lines, "%x  %s\n", digest, relative)
	}
	if err := os.WriteFile(manifest, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ensureLocalSingleNodeExecutionPayloadFixture(t *testing.T, root string, qps int) []string {
	t.Helper()
	files := map[string][]byte{
		"config/effective-wukongim.toml": localSingleNodeReviewedEffectiveConfigFixture(),
		"bin/wukongim":                   []byte("sealed-wukongim-binary"),
		"bin/wkbench":                    []byte("sealed-wkbench-binary"),
	}
	paths := []string{"config/effective-wukongim.toml", "bin/wukongim", "bin/wkbench"}
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, files[relative], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rateTag := fmt.Sprintf("%06d", qps)
	generation := 0
	for index, reviewedQPS := range localbaseline.ReviewedOfferedSendQPS {
		if qps == reviewedQPS {
			generation = index + 1
			break
		}
	}
	if generation == 0 {
		t.Fatalf("fixture qps %d is outside the reviewed staircase", qps)
	}
	preSpawnStage := "pre_spawn"
	if generation == 1 {
		preSpawnStage = "post_ready_first_generation"
	}
	wukongimData, err := os.ReadFile(filepath.Join(root, "bin", "wukongim"))
	if err != nil {
		t.Fatal(err)
	}
	wukongimDigest := digestLocalSingleNodeBytes(wukongimData)
	attestationRelative := fmt.Sprintf("reports/%s-qps/evidence/product-executable.tsv", rateTag)
	attestationPath := filepath.Join(root, filepath.FromSlash(attestationRelative))
	if err := os.MkdirAll(filepath.Dir(attestationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	attestation := fmt.Sprintf("schema\twukongim/chat-lifecycle-local-single-node-product-executable/v1\nbaseline_invocation_id\t0123456789abcdef0123456789abcdef\nrate_tag\t%s\ngeneration\t%d\nbinary\tbin/wukongim\nsource_config_sha256\t%s\npre_spawn_stage\t%s\npre_spawn_sha256\t%s\npost_stop_sha256\t%s\nsealed_binary_sha256\t%s\n",
		rateTag, generation, strings.Repeat("d", 64), preSpawnStage,
		wukongimDigest, wukongimDigest, wukongimDigest,
	)
	if err := os.WriteFile(attestationPath, []byte(attestation), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, attestationRelative)
	return paths
}

func localSingleNodeManifestEntriesFixture(t *testing.T, manifestPath string) []string {
	t.Helper()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed fixture manifest line %q", line)
		}
		entries = append(entries, parts[1])
	}
	return entries
}

func rerunLocalSingleNodeStepFixture(t *testing.T, root string, qps int) (int, string) {
	t.Helper()
	tag := fmt.Sprintf("%06d", qps)
	directory := filepath.Join(root, "reports", tag+"-qps")
	evidenceDirectory := filepath.Join(directory, "evidence")
	for _, name := range []string{"typed-step-evidence.json", "typed-step-result.json"} {
		if err := os.Remove(filepath.Join(evidenceDirectory, name)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	closurePath := filepath.Join(evidenceDirectory, "step-closure.json")
	if err := os.Remove(closurePath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step", "--offered-qps", strconv.Itoa(qps),
		"--required-active-connections", "2500", "--group-members", "10", "--warmup-seconds", "60",
		"--measured-seconds", "300", "--drain-budget-seconds", "90", "--maximum-sample-gap-seconds", "30",
		"--scenario", filepath.Join(directory, "scenario.yaml"), "--plan", filepath.Join(directory, "plan.json"),
		"--run-report", filepath.Join(directory, "report.json"), "--diagnostic-summary", filepath.Join(directory, "diagnostic-summary.json"),
		"--lifecycle", filepath.Join(evidenceDirectory, "lifecycle.jsonl"),
		"--post-warmup-metrics", filepath.Join(evidenceDirectory, "127_0_0_1_5001-post-warmup.prom"),
		"--terminal-metrics", filepath.Join(evidenceDirectory, "terminal.prom"),
		"--storage-overlap", filepath.Join(evidenceDirectory, "storage-overlap.tsv"),
		"--storage-summary", filepath.Join(evidenceDirectory, "storage-summary.tsv"),
		"--host-io-summary", filepath.Join(evidenceDirectory, "host-io-summary.tsv"),
		"--profile-status", filepath.Join(evidenceDirectory, "threshold-pprof-status.json"),
		"--payload-root", root, "--payload-manifest", filepath.Join(evidenceDirectory, "step-checksums.sha256"),
		"--output", filepath.Join(evidenceDirectory, "typed-step-evidence.json"),
		"--result-output", filepath.Join(evidenceDirectory, "typed-step-result.json"), "--closure-output", closurePath,
	}, &stderr)
	return code, stderr.String()
}

func writeLocalSingleNodeDiagnosticFixture(t *testing.T, path string, step localbaseline.StepEvidence) {
	t.Helper()
	diagnostic := map[string]any{
		"schema": "wukongim/wkbench-diagnostic-summary/v1", "run_id": step.RunID,
		"status": benchreport.StatusPassed, "exit_code": benchreport.ExitSuccess,
		"stability_verdict": benchreport.VerdictInsufficientEvidence,
		"phase_windows": []localbaseline.PhaseWindow{
			{Phase: "warmup", StartedAt: step.Timeline.Warmup.StartedAt, EndedAt: step.Timeline.Warmup.EndedAt},
			{Phase: "run", StartedAt: step.Timeline.Measured.StartedAt, EndedAt: step.Timeline.Measured.EndedAt},
			{Phase: "cooldown", StartedAt: step.Timeline.Drain.StartedAt, EndedAt: step.Timeline.Drain.EndedAt},
		},
	}
	data, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLocalSingleNodeLifecycleFixture(t *testing.T, path string, step localbaseline.StepEvidence) {
	t.Helper()
	captures := make([]localbaseline.LifecycleCapture, 0)
	appendPhase := func(name string, phase localbaseline.PhaseEvidence) {
		for _, sample := range phase.Samples {
			captures = append(captures, localbaseline.LifecycleCapture{
				Schema: localbaseline.LifecycleCaptureSchema, SampledAt: sample.ObservedAt,
				Status: &localbaseline.CapturedStatus{
					Phase: name, ActivePhase: name, ObservedAt: sample.ObservedAt,
					Lifecycle: &localbaseline.CapturedLifecycleStatus{
						ActiveConnections:  sample.ActiveConnections,
						ReceiveDrainSHA256: benchmodel.ReceiveDrainFingerprint(sample.ReceiveDrain),
						Traffic:            sample.Traffic,
						ReceiveDrain:       sample.ReceiveDrain,
					},
					Assignment: localbaseline.CapturedAssignment{RunID: step.RunID, AssignmentID: "assignment-1"},
				},
				Server: sample.Server, Worker: sample.Worker,
			})
		}
	}
	appendPhase("warmup", step.Timeline.Warmup)
	appendPhase("run", step.Timeline.Measured)
	appendPhase("cooldown", step.Timeline.Drain)
	terminalAt := step.Timeline.Drain.EndedAt.Add(time.Second)
	captures = append(captures, localbaseline.LifecycleCapture{
		Schema: localbaseline.LifecycleCaptureSchema, SampledAt: terminalAt,
		Status: &localbaseline.CapturedStatus{
			Phase: "stopped", ObservedAt: terminalAt,
			Lifecycle: &localbaseline.CapturedLifecycleStatus{
				ActiveConnections:   step.RequiredActiveConnections,
				ReceiveDrainSHA256:  benchmodel.ReceiveDrainFingerprint(step.Timeline.Terminal.ReceiveDrain),
				TerminalPreClose:    true,
				TerminalCutRequired: true,
				TerminalCutReady:    true,
				TerminalCut:         step.Timeline.Terminal.TerminalCut,
				Traffic:             step.Traffic,
				ReceiveDrain:        step.Timeline.Terminal.ReceiveDrain,
			},
			Assignment: localbaseline.CapturedAssignment{RunID: step.RunID, AssignmentID: "assignment-1"},
		},
		Server: step.Timeline.Terminal.Server,
		Worker: step.Timeline.Terminal.Worker,
	})
	var lines strings.Builder
	for _, capture := range captures {
		data, err := json.Marshal(capture)
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(data)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func localSingleNodeProductQueueMetrics(cut localbaseline.ProductQueueCut) string {
	metadata, _ := json.Marshal(cut)
	return "# wkbench_local_single_node_cut " + string(metadata) + "\n" + `wukongim_gateway_async_send_queue_depth 0
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_runtime_pool_queue_depth{component="channel",pool="append"} 0
wukongim_channelappend_writer_state_items{kind="pending_append"} 0
wukongim_channelappend_writer_state_items{kind="append_inflight"} 0
wukongim_channelappend_writer_state_items{kind="post_commit_backlog"} 0
wukongim_channelappend_post_commit_handoff_depth 0
wukongim_channelappend_post_commit_retry_queue_depth 0
wukongim_channelappend_effect_pool_inflight{stage="append"} 0
wukongim_storage_commit_queue_depth 0
wukongim_delivery_recipient_worker_queue_depth 0
wukongim_delivery_recipient_worker_inflight 0
wukongim_delivery_ack_bindings 0
` + localSingleNodeProductResultCounterMetrics()
}

func localSingleNodeProductResultCounterMetrics() string {
	var builder strings.Builder
	for _, result := range []string{"ok", "panic", "timeout", "canceled", "error", "retry_exhausted", "unknown"} {
		fmt.Fprintf(&builder, "wukongim_delivery_recipient_worker_process_total{result=%q} 0\n", result)
	}
	for _, result := range []string{
		"ok", "mixed", "canceled", "timeout", "backpressured", "channel_busy", "route_not_ready",
		"stale_route", "stale_completion", "not_authority", "not_leader", "channel_not_found",
		"append_result_missing", "append_failed", "commit_failed", "invalid_subscribers", "invalid_cursor",
		"unsupported", "auth_fail", "invalid_request", "system_error", "other",
	} {
		fmt.Fprintf(&builder, "wukongim_channelappend_effect_total{stage=%q,result=%q} 0\n", "post_commit", result)
	}
	return builder.String()
}
