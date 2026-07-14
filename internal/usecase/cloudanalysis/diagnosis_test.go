package cloudanalysis

import (
	"errors"
	"strings"
	"testing"
)

const validDiagnosis = `{
  "schema":"wukongim/cloud-simulation-diagnosis/v1",
  "run_identity":{"run_id":"run-1","source_sha":"0123456789abcdef0123456789abcdef01234567","scenario_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
  "analyzed_window":{"start":"2026-07-14T01:00:00Z","end":"2026-07-14T01:30:00Z"},
  "verdict":"product_defect","severity":"high","confidence":0.9,"root_cause_scope":"product","summary":"bounded summary",
  "observation_references":[{"tool":"metrics_query_range","node":"node-1","observed_at":"2026-07-14T01:30:00Z","window":"01:00 .. 01:30","complete":true}],
  "supporting_signals":["queue saturation"],"contradictory_signals":[],"unresolved_signals":[],
  "remediation_eligibility":{"eligible":true,"reason":"repository path proven","repository_attributable":true,"testable":true},
  "proposed_regression_coverage":["add deterministic queue test"],"cloud_revalidation_required":true
}`

func TestDecodeDiagnosisResultAcceptsStrictValidResult(t *testing.T) {
	result, err := DecodeDiagnosisResult(strings.NewReader(validDiagnosis))
	if err != nil {
		t.Fatalf("DecodeDiagnosisResult() error = %v", err)
	}
	if result.Verdict != "product_defect" || !result.RemediationEligibility.Eligible {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodeDiagnosisResultRejectsUnknownFieldsAndUnsafeEligibility(t *testing.T) {
	unknown := strings.Replace(validDiagnosis, `"schema":`, `"extra":true,"schema":`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(unknown)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("unknown field error = %v", err)
	}
	unsafe := strings.Replace(validDiagnosis, `"verdict":"product_defect"`, `"verdict":"infrastructure_interrupted"`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(unsafe)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("unsafe eligibility error = %v", err)
	}
}

func TestDecodeDiagnosisResultRejectsReversedWindow(t *testing.T) {
	reversed := strings.Replace(validDiagnosis, `"end":"2026-07-14T01:30:00Z"`, `"end":"2026-07-14T00:30:00Z"`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(reversed)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("reversed window error = %v", err)
	}
}

func TestDecodeDiagnosisResultRejectsMissingRequiredFalseAndEmptyArrays(t *testing.T) {
	missingFalse := strings.Replace(validDiagnosis, `,"cloud_revalidation_required":true`, "", 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(missingFalse)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("missing boolean error = %v", err)
	}
	nullArray := strings.Replace(validDiagnosis, `"contradictory_signals":[]`, `"contradictory_signals":null`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(nullArray)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("null array error = %v", err)
	}
	missingComplete := strings.Replace(validDiagnosis, `,"complete":true`, "", 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(missingComplete)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("missing observation boolean error = %v", err)
	}
}

func TestDecodeDiagnosisResultRejectsContradictoryClassificationAndIncompleteEvidence(t *testing.T) {
	wrongScope := strings.Replace(validDiagnosis, `"root_cause_scope":"product"`, `"root_cause_scope":"infrastructure"`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(wrongScope)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("wrong scope error = %v", err)
	}
	noCompleteEvidence := strings.Replace(validDiagnosis, `"complete":true`, `"complete":false`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(noCompleteEvidence)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("incomplete evidence error = %v", err)
	}
	noRevalidation := strings.Replace(validDiagnosis, `"cloud_revalidation_required":true`, `"cloud_revalidation_required":false`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(noRevalidation)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("missing revalidation error = %v", err)
	}
}

func TestDecodeDiagnosisResultRequiresCompleteWorkloadForHealthy(t *testing.T) {
	healthy := strings.NewReplacer(
		`"verdict":"product_defect","severity":"high"`, `"verdict":"healthy","severity":"none"`,
		`"root_cause_scope":"product"`, `"root_cause_scope":"none"`,
		`"tool":"metrics_query_range"`, `"tool":"workload_inspect"`,
		`"complete":true}`, `"complete":true,"state":"completed","status":"passed"}`,
		`"eligible":true`, `"eligible":false`,
		`"repository_attributable":true`, `"repository_attributable":false`,
		`"testable":true`, `"testable":false`,
		`"proposed_regression_coverage":["add deterministic queue test"]`, `"proposed_regression_coverage":[]`,
		`"cloud_revalidation_required":true`, `"cloud_revalidation_required":false`,
	).Replace(validDiagnosis)
	if _, err := DecodeDiagnosisResult(strings.NewReader(healthy)); err != nil {
		t.Fatalf("healthy workload diagnosis error = %v", err)
	}
	withoutWorkload := strings.Replace(healthy, `"tool":"workload_inspect"`, `"tool":"metrics_query_range"`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(withoutWorkload)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("healthy without workload error = %v", err)
	}
	failedWorkload := strings.Replace(healthy, `"status":"passed"`, `"status":"failed"`, 1)
	if _, err := DecodeDiagnosisResult(strings.NewReader(failedWorkload)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("healthy with failed workload error = %v", err)
	}
}
