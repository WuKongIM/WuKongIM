package issueagentgithub

import (
	"testing"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestRebaseStageIdentityChangesAfterLostRaceAndHeadAdoption(t *testing.T) {
	t.Parallel()

	first := RebasePlan{
		Branch:                "agent/issue-42",
		ExpectedOldHeadSHA:    "0123456789abcdef0123456789abcdef01234567",
		CurrentMainSHA:        "1234567890abcdef1234567890abcdef12345678",
		ExpectedResultTreeSHA: "234567890abcdef1234567890abcdef123456789",
		Message:               "chore(agent): rebase issue #42",
		ExpectedAuthorLogin:   "issue-agent[bot]",
		ChangeSet: issueagentcontract.ChangeSet{
			Files: []issueagentcontract.FileChange{{
				Path:          "pkg/exact/fix.go",
				Operation:     issueagentcontract.FileOperationUpsert,
				Mode:          issueagentcontract.FileModeRegular,
				ContentBase64: issueagentcontract.EncodeFileContent([]byte("first\n")),
			}},
		},
	}
	orphanedStage, err := rebaseStageBranch(first)
	require.NoError(t, err)
	require.True(t, agentStageRefPattern.MatchString(orphanedStage))

	afterAdopt := first
	afterAdopt.ExpectedOldHeadSHA =
		"34567890abcdef1234567890abcdef1234567890"
	afterAdopt.ExpectedResultTreeSHA =
		"4567890abcdef1234567890abcdef12345678901"
	afterAdopt.ChangeSet.Files[0].ContentBase64 =
		issueagentcontract.EncodeFileContent([]byte("second\n"))
	retryStage, err := rebaseStageBranch(afterAdopt)
	require.NoError(t, err)
	require.True(t, agentStageRefPattern.MatchString(retryStage))
	require.NotEqual(t, orphanedStage, retryStage,
		"an orphaned failed-CAS candidate must not block a new exact effect")
}
