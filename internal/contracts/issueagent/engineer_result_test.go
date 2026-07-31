package issueagent_test

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

const validEngineerResultJSON = `{"schema_version":2,"repository":"WuKongIM/WuKongIM",` +
	`"issue_number":42,` +
	`"task_id":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"outcome":"ready","external_symptom":"server exits",` +
	`"root_cause":"nil route target","causal_path":"reconnect -> route -> panic",` +
	`"evidence_references":["internal/runtime/example.go:42"],` +
	`"proposed_risk":["low"],"tests_attempted":["go test ./internal/runtime/example"],` +
	`"unresolved_uncertainty":"","summary":"guard missing route target",` +
	`"ready":true}`

func TestDecodeEngineerResultAcceptsProseBeforeSingleJSONObject(t *testing.T) {
	t.Parallel()

	result, err := issueagent.DecodeEngineerResult(strings.NewReader(
		"All verification complete; the focused suite passes.\n\n"+
			validEngineerResultJSON,
	), 16<<10)
	require.NoError(t, err)
	require.Equal(t, issueagent.EngineerOutcomeReady, result.Outcome)
	require.True(t, result.Ready)
}

func TestDecodeEngineerResultAcceptsSingleJSONFence(t *testing.T) {
	t.Parallel()

	result, err := issueagent.DecodeEngineerResult(strings.NewReader(
		"The fix is complete and verified.\n\n```json\n"+
			validEngineerResultJSON+
			"\n```\n"),
		16<<10,
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.EngineerOutcomeReady, result.Outcome)
	require.True(t, result.Ready)
}

func TestDecodeEngineerResultRejectsMultipleJSONFences(t *testing.T) {
	t.Parallel()

	_, err := issueagent.DecodeEngineerResult(strings.NewReader(
		"```json\n"+validEngineerResultJSON+"\n```\n```json\n"+
			validEngineerResultJSON+"\n```\n",
	), 32<<10)
	require.Error(t, err)
}

func TestDecodeEngineerResultRejectsRawAndFencedResults(t *testing.T) {
	t.Parallel()

	_, err := issueagent.DecodeEngineerResult(strings.NewReader(
		validEngineerResultJSON+"\n```json\n"+validEngineerResultJSON+"\n```\n",
	), 32<<10)
	require.Error(t, err)
}

func TestDecodeEngineerResultRejectsAmbiguousProseWrappedJSON(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"multiple objects": validEngineerResultJSON + "\n{}",
		"array wrapper":    "[" + validEngineerResultJSON + "]",
		"object in prose":  "Use {strict} output.\n" + validEngineerResultJSON,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := issueagent.DecodeEngineerResult(
				strings.NewReader(input),
				int64(len(input)),
			)
			require.Error(t, err)
		})
	}
}

func TestDecodeEngineerResultRejectsTrustedTestClaim(t *testing.T) {
	t.Parallel()

	_, err := issueagent.DecodeEngineerResult(strings.NewReader(
		`{"schema_version":2,"repository":"WuKongIM/WuKongIM",`+
			`"issue_number":42,`+
			`"task_id":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"outcome":"ready","external_symptom":"server exits",`+
			`"root_cause":"nil route target","causal_path":"reconnect -> route -> panic",`+
			`"evidence_references":["internal/runtime/example.go:42"],`+
			`"proposed_risk":["low"],"tests_attempted":["go test ./internal/runtime/example"],`+
			`"unresolved_uncertainty":"","summary":"guard missing route target",`+
			`"ready":true,"test_passed":true}`),
		16<<10,
	)
	require.EqualError(t, err, `decode JSON input: json: unknown field "test_passed"`)
}

func TestValidateEngineerResultRequiresReadyEvidenceAndTests(t *testing.T) {
	t.Parallel()

	result := issueagent.EngineerResult{
		SchemaVersion:   2,
		Repository:      "WuKongIM/WuKongIM",
		IssueNumber:     42,
		TaskID:          "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Outcome:         issueagent.EngineerOutcomeReady,
		ExternalSymptom: "server exits",
		RootCause:       "nil route target",
		CausalPath:      "reconnect -> route -> panic",
		ProposedRisk:    []string{"low"},
		Summary:         "guard missing route target",
		Ready:           true,
	}
	require.EqualError(t, issueagent.ValidateEngineerResult(result),
		"ready Engineer result lacks diagnosis or test references")
}
