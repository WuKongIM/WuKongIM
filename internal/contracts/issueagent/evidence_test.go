package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCandidateEvidenceAllowsPublicationOnlyForVerifiedLowRiskChange(t *testing.T) {
	t.Parallel()

	evidence := issueagent.CandidateEvidence{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		TaskID:              "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BaseSHA:             "0123456789abcdef0123456789abcdef01234567",
		CandidateDigest:     "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ChangeSetDigest:     "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Risk:                issueagent.CandidateRiskLow,
		PublicationEligible: true,
		RequiredSuites:      []string{"focused", "unit"},
		Commands: []issueagent.VerificationCommand{{
			Arguments:    []string{"go", "test", "./internal/runtime/example", "-count=1"},
			WorkingDir:   ".",
			ExitCode:     0,
			StdoutDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			StderrDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			DurationMS:   125,
		}},
		CreatedAt: time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}

	require.NoError(t, issueagent.ValidateCandidateEvidence(evidence))

	evidence.Commands[0].ExitCode = 1
	require.EqualError(t, issueagent.ValidateCandidateEvidence(evidence),
		"publishable Candidate Evidence contains a failed command")
}
