package localbaseline

import (
	"strings"
	"testing"
)

func TestValidateStepClosureRecomputesDecision(t *testing.T) {
	evidence := completeStepEvidence(1000)
	closure := StepClosure{
		Schema: StepClosureSchema, ClosureManifest: "reports/1000-qps/evidence/step-closure.json",
		ClosureManifestSHA256: strings.Repeat("a", 64), Evidence: evidence,
		Result: CloseStepResult(evidence, strings.Repeat("b", 64)),
	}
	if !ValidateStepClosure(closure) {
		t.Fatal("complete closure was rejected")
	}

	closure.Result.Clean = false
	if ValidateStepClosure(closure) {
		t.Fatal("caller-authored closed decision was accepted")
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsRawOrTamperedStepClosure(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.StepClosures[1].Result.ActualOfferedRatio = 0
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonSeal) {
		t.Fatalf("authorization = %+v, want closure seal failure", result)
	}
}

func TestBaselineCompletionGenerationBindsEveryClosure(t *testing.T) {
	evidence := completeBaselineEvidence()
	original := evidence.CompletionGeneration
	evidence.StepClosures[0].ClosureManifestSHA256 = strings.Repeat("f", 64)
	if evidence.CompletionGeneration == baselineCompletionGeneration(evidence) {
		t.Fatal("changed closure retained the published generation")
	}
	SealBaselineEvidence(&evidence)
	if evidence.CompletionGeneration == original || evidence.CompletionGeneration != baselineCompletionGeneration(evidence) {
		t.Fatalf("completion generation = %q, original = %q", evidence.CompletionGeneration, original)
	}
}

func TestValidateStepClosureManifestRequiresCanonicalExecutionPayloadPaths(t *testing.T) {
	manifest := completeStepClosureManifestFixture()
	if !ValidateStepClosureManifest(manifest) {
		t.Fatal("complete closure manifest was rejected")
	}
	for name, mutate := range map[string]func(*StepClosureManifest){
		"config":  func(value *StepClosureManifest) { value.Inputs.EffectiveConfig = "config/copy.toml" },
		"server":  func(value *StepClosureManifest) { value.Inputs.WukongIMBinary = "bin/server-copy" },
		"wkbench": func(value *StepClosureManifest) { value.Inputs.WkbenchBinary = "bin/wkbench-copy" },
		"product executable": func(value *StepClosureManifest) {
			value.Inputs.ProductExecutable = "reports/000500-qps/evidence/product-executable.tsv"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := manifest
			mutate(&changed)
			if ValidateStepClosureManifest(changed) {
				t.Fatal("non-canonical execution payload path was accepted")
			}
		})
	}
}

func completeStepClosureManifestFixture() StepClosureManifest {
	digest := strings.Repeat("a", 64)
	return StepClosureManifest{
		Schema: StepClosureManifestSchema, PayloadManifest: "reports/000250-qps/evidence/step-checksums.sha256", PayloadSHA256: digest,
		Inputs: StepClosureInputs{
			Scenario: "reports/000250-qps/scenario.yaml", Plan: "reports/000250-qps/plan.json", Report: "reports/000250-qps/report.json",
			EffectiveConfig: "config/effective-wukongim.toml", WukongIMBinary: "bin/wukongim", WkbenchBinary: "bin/wkbench",
			ProductExecutable: "reports/000250-qps/evidence/product-executable.tsv",
			DiagnosticSummary: "reports/000250-qps/diagnostic-summary.json", Lifecycle: "reports/000250-qps/evidence/lifecycle.jsonl",
			PostWarmupMetrics: "metrics/000250/127_0_0_1_5001-post-warmup.prom", TerminalMetrics: "metrics/000250/terminal.prom",
			StorageOverlap: "reports/000250-qps/evidence/storage-overlap.tsv", StorageSummary: "reports/000250-qps/evidence/storage-summary.tsv",
			HostIOSummary: "reports/000250-qps/evidence/host-io-summary.tsv", ProfileStatus: "reports/000250-qps/evidence/threshold-pprof-status.json",
		},
		Settings: StepClosureSettings{
			OfferedSendQPS: 250, RequiredActiveConnections: 2500, ConfiguredGroupMembers: 10,
			ConfiguredWarmupSeconds: 60, ConfiguredMeasuredSeconds: 300, ConfiguredDrainBudgetSeconds: 90, MaximumSampleGapSeconds: 30,
		},
		Evidence: "reports/000250-qps/evidence/typed-step-evidence.json", EvidenceSHA256: digest,
		Result: "reports/000250-qps/evidence/typed-step-result.json", ResultSHA256: digest,
	}
}
