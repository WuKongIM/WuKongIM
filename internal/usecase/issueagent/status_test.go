package issueagent_test

import (
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestRenderIssueStatusShowsOneHumanFacingEngineeringState(t *testing.T) {
	t.Parallel()

	body, err := issueagent.RenderIssueStatus(contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            2,
		State:               contract.IssueStateEngineering,
		Reason:              "authorized low-risk Bug is ready for engineering",
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Task: &contract.TaskIdentity{
			ID:           "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Kind:         contract.TaskKindEngineer,
			BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
			AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
			PolicyDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			PromptDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
		Authorization: &contract.AuthorizationRecord{
			Actor: "maintainer", Permission: "write",
			EventID: "issue:42", Command: "/agent fix",
		},
		UpdatedAt: time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, `<!-- wukongim-issue-agent-status -->
### Issue Agent

Engineering — reproducing, diagnosing, fixing, and testing this Issue in one bounded run.
`, body)
	require.NotContains(t, body, "CAS")
	require.NotContains(t, body, "lease")
}
