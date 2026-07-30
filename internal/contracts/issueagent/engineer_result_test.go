package issueagent_test

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

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
