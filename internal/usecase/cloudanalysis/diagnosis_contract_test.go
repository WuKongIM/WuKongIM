package cloudanalysis

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestDiagnosisResultRejectsNonFiniteConfidence(t *testing.T) {
	result, err := DecodeDiagnosisResult(strings.NewReader(validDiagnosis))
	if err != nil {
		t.Fatalf("DecodeDiagnosisResult(valid) error = %v", err)
	}

	for name, confidence := range map[string]float64{
		"nan":      math.NaN(),
		"positive": math.Inf(1),
		"negative": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := result
			candidate.Confidence = confidence
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidDiagnosis) {
				t.Fatalf("Validate() error = %v, want %v for confidence %v", err, ErrInvalidDiagnosis, confidence)
			}
		})
	}
}

func TestDecodeDiagnosisResultRejectsUnboundedOrStructurallyAmbiguousInput(t *testing.T) {
	if _, err := DecodeDiagnosisResult(nil); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("DecodeDiagnosisResult(nil) error = %v, want %v", err, ErrInvalidDiagnosis)
	}
	readFailure := errors.New("read failed")
	if _, err := DecodeDiagnosisResult(diagnosisFailReader{err: readFailure}); !errors.Is(err, ErrInvalidDiagnosis) || !strings.Contains(err.Error(), readFailure.Error()) {
		t.Fatalf("DecodeDiagnosisResult(read failure) error = %v", err)
	}
	if _, err := DecodeDiagnosisResult(strings.NewReader(strings.Repeat(" ", maxDiagnosisBytes+1))); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("DecodeDiagnosisResult(oversized) error = %v, want %v", err, ErrInvalidDiagnosis)
	}
	if _, err := DecodeDiagnosisResult(strings.NewReader(validDiagnosis + ` {}`)); !errors.Is(err, ErrInvalidDiagnosis) {
		t.Fatalf("DecodeDiagnosisResult(trailing document) error = %v, want %v", err, ErrInvalidDiagnosis)
	}

	tests := []struct {
		name     string
		document func(*testing.T) string
	}{
		{name: "root array", document: func(*testing.T) string { return `[]` }},
		{name: "malformed root object", document: func(*testing.T) string { return `{` }},
		{name: "run identity is not an object", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["run_identity"] = []any{}
		})},
		{name: "run identity omits source commit", document: mutateDiagnosisDocument(func(root map[string]any) {
			delete(root["run_identity"].(map[string]any), "source_sha")
		})},
		{name: "analysis window is not an object", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["analyzed_window"] = []any{}
		})},
		{name: "analysis window omits start", document: mutateDiagnosisDocument(func(root map[string]any) {
			delete(root["analyzed_window"].(map[string]any), "start")
		})},
		{name: "eligibility is not an object", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["remediation_eligibility"] = []any{}
		})},
		{name: "eligibility omits testability", document: mutateDiagnosisDocument(func(root map[string]any) {
			delete(root["remediation_eligibility"].(map[string]any), "testable")
		})},
		{name: "observation references are not an array", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["observation_references"] = "metrics_query_range"
		})},
		{name: "observation reference is not an object", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["observation_references"] = []any{"metrics_query_range"}
		})},
		{name: "signal list is not an array", document: mutateDiagnosisDocument(func(root map[string]any) {
			root["supporting_signals"] = "queue saturation"
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeDiagnosisResult(strings.NewReader(tt.document(t))); !errors.Is(err, ErrInvalidDiagnosis) {
				t.Fatalf("DecodeDiagnosisResult() error = %v, want %v", err, ErrInvalidDiagnosis)
			}
		})
	}
}

func TestDiagnosisVerdictsRequireConsistentEvidenceOwnership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DiagnosisResult)
	}{
		{
			name: "infrastructure interruption",
			mutate: func(result *DiagnosisResult) {
				result.Verdict = VerdictInfrastructureInterrupted
				result.RootCauseScope = RootCauseInfrastructure
				result.RemediationEligibility = RemediationEligibility{Reason: "cloud interruption is not repository-remediable"}
				result.ProposedRegressionCoverage = []string{}
				result.CloudRevalidationRequired = false
			},
		},
		{
			name: "invalid scenario",
			mutate: func(result *DiagnosisResult) {
				result.Verdict = VerdictScenarioInvalid
				result.RootCauseScope = RootCauseScenario
				result.RemediationEligibility = RemediationEligibility{Reason: "scenario must be corrected first"}
				result.ProposedRegressionCoverage = []string{}
				result.CloudRevalidationRequired = false
			},
		},
		{
			name: "insufficient evidence",
			mutate: func(result *DiagnosisResult) {
				result.Verdict = VerdictInsufficientEvidence
				result.Severity = SeverityNone
				result.RootCauseScope = RootCauseUnknown
				result.ObservationReferences = []DiagnosisObservationReference{}
				result.SupportingSignals = []string{}
				result.UnresolvedSignals = []string{"terminal workload evidence is unavailable"}
				result.RemediationEligibility = RemediationEligibility{Reason: "evidence is incomplete"}
				result.ProposedRegressionCoverage = []string{}
				result.CloudRevalidationRequired = false
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mustDiagnosisResult(t)
			tt.mutate(&result)
			if err := result.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestDiagnosisRejectsContradictoryOrUnboundedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DiagnosisResult)
	}{
		{name: "product defect with no severity", mutate: func(result *DiagnosisResult) {
			result.Severity = SeverityNone
		}},
		{name: "infrastructure interruption with no severity", mutate: func(result *DiagnosisResult) {
			result.Verdict = VerdictInfrastructureInterrupted
			result.Severity = SeverityNone
			result.RootCauseScope = RootCauseInfrastructure
			result.RemediationEligibility = RemediationEligibility{Reason: "not eligible"}
		}},
		{name: "scenario defect with no severity", mutate: func(result *DiagnosisResult) {
			result.Verdict = VerdictScenarioInvalid
			result.Severity = SeverityNone
			result.RootCauseScope = RootCauseScenario
			result.RemediationEligibility = RemediationEligibility{Reason: "not eligible"}
		}},
		{name: "insufficient evidence with impact severity", mutate: func(result *DiagnosisResult) {
			result.Verdict = VerdictInsufficientEvidence
			result.Severity = SeverityLow
			result.RootCauseScope = RootCauseUnknown
			result.RemediationEligibility = RemediationEligibility{Reason: "not eligible"}
		}},
		{name: "unknown workload lifecycle", mutate: func(result *DiagnosisResult) {
			result.ObservationReferences[0].Tool = "workload_inspect"
			result.ObservationReferences[0].State = "paused"
		}},
		{name: "too many observation references", mutate: func(result *DiagnosisResult) {
			ref := result.ObservationReferences[0]
			result.ObservationReferences = make([]DiagnosisObservationReference, 33)
			for i := range result.ObservationReferences {
				result.ObservationReferences[i] = ref
			}
		}},
		{name: "observation without tool identity", mutate: func(result *DiagnosisResult) {
			result.ObservationReferences[0].Tool = ""
		}},
		{name: "too many supporting signals", mutate: func(result *DiagnosisResult) {
			result.SupportingSignals = make([]string, 25)
			for i := range result.SupportingSignals {
				result.SupportingSignals[i] = "bounded signal"
			}
		}},
		{name: "empty supporting signal", mutate: func(result *DiagnosisResult) {
			result.SupportingSignals = []string{""}
		}},
		{name: "nul in summary", mutate: func(result *DiagnosisResult) {
			result.Summary = "unsafe\x00summary"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mustDiagnosisResult(t)
			tt.mutate(&result)
			if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnosis) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidDiagnosis)
			}
		})
	}
}

func mustDiagnosisResult(t *testing.T) DiagnosisResult {
	t.Helper()
	result, err := DecodeDiagnosisResult(strings.NewReader(validDiagnosis))
	if err != nil {
		t.Fatalf("DecodeDiagnosisResult(valid) error = %v", err)
	}
	return result
}

func mutateDiagnosisDocument(mutate func(map[string]any)) func(*testing.T) string {
	return func(t *testing.T) string {
		t.Helper()
		var root map[string]any
		if err := json.Unmarshal([]byte(validDiagnosis), &root); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		mutate(root)
		encoded, err := json.Marshal(root)
		if err != nil {
			t.Fatalf("encode fixture: %v", err)
		}
		return string(encoded)
	}
}

type diagnosisFailReader struct {
	err error
}

func (r diagnosisFailReader) Read([]byte) (int, error) {
	return 0, r.err
}
