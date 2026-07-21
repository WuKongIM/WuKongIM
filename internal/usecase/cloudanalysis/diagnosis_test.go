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
  "observation_references":[{"tool":"metrics_query_range","node":"node-1","observed_at":"2026-07-14T01:30:00Z","window":"01:00 .. 01:30","complete":true,"state":null,"status":null,"note":null}],
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

func TestDecodeDiagnosisResultAcceptsIncompleteWorkloadWithNullableState(t *testing.T) {
	incomplete := `{
  "schema":"wukongim/cloud-simulation-diagnosis/v1",
  "run_identity":{"run_id":"run-1","source_sha":"0123456789abcdef0123456789abcdef01234567","scenario_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
  "analyzed_window":{"start":"2026-07-14T01:00:00Z","end":"2026-07-14T01:30:00Z"},
  "verdict":"insufficient_evidence","severity":"none","confidence":0.8,"root_cause_scope":"unknown","summary":"The workload summary could not be decoded.",
  "observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T01:30:00Z","window":"final","complete":false,"state":null,"status":null,"note":"summary contract mismatch"}],
  "supporting_signals":[],"contradictory_signals":[],"unresolved_signals":["terminal workload evidence unavailable"],
  "remediation_eligibility":{"eligible":false,"reason":"evidence is incomplete","repository_attributable":false,"testable":false},
  "proposed_regression_coverage":[],"cloud_revalidation_required":true
}`

	result, err := DecodeDiagnosisResult(strings.NewReader(incomplete))
	if err != nil {
		t.Fatalf("DecodeDiagnosisResult() error = %v", err)
	}
	if result.Verdict != VerdictInsufficientEvidence || result.ObservationReferences[0].Complete {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodeDiagnosisResultRejectsEmptyWorkloadStateAndStatus(t *testing.T) {
	incomplete := `{
  "schema":"wukongim/cloud-simulation-diagnosis/v1",
  "run_identity":{"run_id":"run-1","source_sha":"0123456789abcdef0123456789abcdef01234567","scenario_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
  "analyzed_window":{"start":"2026-07-14T01:00:00Z","end":"2026-07-14T01:30:00Z"},
  "verdict":"insufficient_evidence","severity":"none","confidence":0.8,"root_cause_scope":"unknown","summary":"The workload summary could not be decoded.",
  "observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T01:30:00Z","window":"final","complete":false,"state":null,"status":null,"note":null}],
  "supporting_signals":[],"contradictory_signals":[],"unresolved_signals":["terminal workload evidence unavailable"],
  "remediation_eligibility":{"eligible":false,"reason":"evidence is incomplete","repository_attributable":false,"testable":false},
  "proposed_regression_coverage":[],"cloud_revalidation_required":true
}`
	for _, field := range []string{"state", "status"} {
		empty := strings.Replace(incomplete, `"`+field+`":null`, `"`+field+`":""`, 1)
		if _, err := DecodeDiagnosisResult(strings.NewReader(empty)); !errors.Is(err, ErrInvalidDiagnosis) {
			t.Fatalf("empty %s error = %v, want %v", field, err, ErrInvalidDiagnosis)
		}
	}
}

func TestDecodeDiagnosisResultRejectsWorkloadCompletenessLifecycleMismatch(t *testing.T) {
	base := `{
  "schema":"wukongim/cloud-simulation-diagnosis/v1",
  "run_identity":{"run_id":"run-1","source_sha":"0123456789abcdef0123456789abcdef01234567","scenario_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
  "analyzed_window":{"start":"2026-07-14T01:00:00Z","end":"2026-07-14T01:30:00Z"},
  "verdict":"insufficient_evidence","severity":"none","confidence":0.8,"root_cause_scope":"unknown","summary":"The workload summary could not be decoded.",
  "observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T01:30:00Z","window":"final","complete":false,"state":null,"status":null,"note":"summary contract mismatch"}],
  "supporting_signals":[],"contradictory_signals":[],"unresolved_signals":["terminal workload evidence unavailable"],
  "remediation_eligibility":{"eligible":false,"reason":"evidence is incomplete","repository_attributable":false,"testable":false},
  "proposed_regression_coverage":[],"cloud_revalidation_required":true
}`
	cases := map[string]string{
		"complete in progress": strings.Replace(base, `"complete":false,"state":null,"status":null`, `"complete":true,"state":"in_progress","status":null`, 1),
		"incomplete terminal":  strings.Replace(base, `"complete":false,"state":null,"status":null`, `"complete":false,"state":"completed","status":"passed"`, 1),
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeDiagnosisResult(strings.NewReader(document)); !errors.Is(err, ErrInvalidDiagnosis) {
				t.Fatalf("DecodeDiagnosisResult() error = %v, want %v", err, ErrInvalidDiagnosis)
			}
		})
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
	for _, field := range []string{"state", "status", "note"} {
		missingNullable := strings.Replace(validDiagnosis, `,"`+field+`":null`, "", 1)
		if _, err := DecodeDiagnosisResult(strings.NewReader(missingNullable)); !errors.Is(err, ErrInvalidDiagnosis) {
			t.Fatalf("missing nullable observation field %s error = %v", field, err)
		}
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
		`"complete":true,"state":null,"status":null,"note":null}`, `"complete":true,"state":"completed","status":"passed","note":null}`,
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
