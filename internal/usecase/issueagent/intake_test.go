package issueagent_test

import (
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

const completeBugBody = `### Environment, topology, and client

Linux, three-node cluster, Go SDK

### Reproduction steps

1. Connect
2. Send

### Expected and actual result

Expected delivery; observed a timeout.
`

func TestAssessBugIssueAcceptsCompleteTemplate(t *testing.T) {
	t.Parallel()

	complete, reason := issueagent.AssessBugIssue(
		"[BUG] delivery timeout",
		completeBugBody,
		[]string{"bug", "needs-triage"},
		"",
	)
	require.True(t, complete)
	require.Empty(t, reason)
}

func TestAssessBugIssueRejectsNonBugAndMissingSections(t *testing.T) {
	t.Parallel()

	complete, reason := issueagent.AssessBugIssue(
		"Feature request",
		completeBugBody,
		[]string{"bug"},
		"",
	)
	require.False(t, complete)
	require.Contains(t, reason, "[BUG]")

	complete, reason = issueagent.AssessBugIssue(
		"[BUG] timeout",
		"### Reproduction steps\n\n_No response_\n",
		[]string{"bug"},
		"",
	)
	require.False(t, complete)
	require.Contains(t, reason, "Environment")
}

func TestClassifyIssueRiskUsesProtectedTopics(t *testing.T) {
	t.Parallel()

	require.Equal(t, contract.CandidateRiskInvestigation,
		issueagent.ClassifyIssueRisk(
			"[BUG] workflow fails",
			"Changes GitHub Actions credentials",
			[]string{"bug"},
			[]string{"github actions"},
		),
	)
	require.Equal(t, contract.CandidateRiskLow,
		issueagent.ClassifyIssueRisk(
			"[BUG] reconnect timeout",
			"Ordinary runtime behavior",
			[]string{"bug"},
			[]string{"github actions"},
		),
	)
}
