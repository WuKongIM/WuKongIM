package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCheckpointSuccessorPinsAffectedSHAWithoutRewritingFrozenFacts(t *testing.T) {
	t.Parallel()

	previous := successorBaseCheckpoint()
	previous.State = issueagent.StateAuthorized
	previous.NextAction = issueagent.ActionPinVersions
	previous.Versions.AffectedSHA = ""
	next := previous
	next.Sequence++
	next.State = issueagent.StateVersionPinned
	next.NextAction = issueagent.ActionReproduce
	next.Versions.AffectedSHA = "89abcdef0123456789abcdef0123456789abcdef"
	previousID := int64(1)
	previousDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	next.ExpectedPreviousCheckpointID = &previousID
	next.PreviousCheckpointSHA256 = &previousDigest

	require.NoError(t, issueagent.ValidateCheckpointSuccessor(previous, next))
	next.FrozenInput.AuthorizedBy = "other"
	require.Error(t, issueagent.ValidateCheckpointSuccessor(previous, next))
}

func TestCheckpointSuccessorRejectsReproductionMutationAndBudgetRollback(t *testing.T) {
	t.Parallel()

	previous := checkpointWithReproduction()
	require.NoError(t, issueagent.ValidateCheckpoint(previous))
	next := previous
	next.Sequence++
	next.State = issueagent.StateDraftPROpen
	next.NextAction = issueagent.ActionDiagnose
	previousID := int64(2)
	previousDigest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	next.ExpectedPreviousCheckpointID = &previousID
	next.PreviousCheckpointSHA256 = &previousDigest
	next.Work = &issueagent.Work{
		Branch: previous.Work.Branch, HeadSHA: previous.Work.HeadSHA, PRNumber: 9,
	}
	require.NoError(t, issueagent.ValidateCheckpointSuccessor(previous, next))

	mutated := next
	mutated.Reproduction = cloneReproduction(next.Reproduction)
	mutated.Reproduction.Assertion = "weakened"
	require.Error(t, issueagent.ValidateCheckpointSuccessor(previous, mutated))

	rolledBack := next
	previous.Budget.WorkerSeconds = 10
	rolledBack.Budget.WorkerSeconds = 9
	require.Error(t, issueagent.ValidateCheckpointSuccessor(previous, rolledBack))
}

func TestMaintainerControlGenerationCannotRewriteDomainFacts(t *testing.T) {
	t.Parallel()

	previous := successorBaseCheckpoint()
	next := previous
	next.Generation++
	next.Sequence++
	next.State = issueagent.StateCancelled
	next.NextAction = issueagent.ActionNone
	previousID := int64(10)
	previousDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	next.ExpectedPreviousCheckpointID = &previousID
	next.PreviousCheckpointSHA256 = &previousDigest
	next.Control = &issueagent.ControlAudit{
		Kind: "cancel", EventID: "comment-11", Actor: "maintainer",
		CommentID: 11,
	}
	require.NoError(t, issueagent.ValidateCheckpointSuccessor(previous, next))

	next.Versions.AffectedSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.Error(t, issueagent.ValidateCheckpointSuccessor(previous, next))
}

func TestHumanMergeAndBudgetHandoffTransitions(t *testing.T) {
	t.Parallel()

	require.NoError(t, issueagent.ValidateTransition(
		issueagent.StateReadyForReview, issueagent.StateMerged,
	))
	require.Error(t, issueagent.ValidateTransition(
		issueagent.StateValidating, issueagent.StateMerged,
	))
	require.NoError(t, issueagent.ValidateTransition(
		issueagent.StateVersionPinned, issueagent.StateReadyForHuman,
	))
}

func TestRecoveryGenerationCanOnlyReturnToItsDurableBoundary(t *testing.T) {
	t.Parallel()

	previous := successorBaseCheckpoint()
	next := previous
	next.Generation++
	next.Sequence++
	previousID := int64(10)
	previousDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	next.ExpectedPreviousCheckpointID = &previousID
	next.PreviousCheckpointSHA256 = &previousDigest
	next.Control = &issueagent.ControlAudit{
		Kind: "recover_chain", EventID: "comment-12", Actor: "admin",
		CommentID: 12, RecoveryAnchorCommentID: 10,
		RecoveryAnchorDigest:  previousDigest,
		QuarantinedCommentIDs: []int64{11},
		QuarantineDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	require.NoError(t, issueagent.ValidateCheckpointSuccessor(previous, next))

	next.State = issueagent.StateVersionPinned
	next.NextAction = issueagent.ActionReproduce
	require.Error(t, issueagent.ValidateCheckpointSuccessor(previous, next))
}

func checkpointWithReproduction() issueagent.Checkpoint {
	checkpoint := successorBaseCheckpoint()
	checkpoint.Sequence = 2
	previousID := int64(1)
	previousDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	checkpoint.ExpectedPreviousCheckpointID = &previousID
	checkpoint.PreviousCheckpointSHA256 = &previousDigest
	checkpoint.State = issueagent.StateReproduced
	checkpoint.NextAction = issueagent.ActionOpenDraftPR
	checkpoint.Reproduction = &issueagent.Reproduction{
		TestFiles: []issueagent.TestFile{{
			Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
			BlobSHA: "0123456789abcdef0123456789abcdef01234567",
		}},
		Assertion:       "delivery succeeds",
		AssertionSHA256: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Topology:        "single-node-cluster",
		AffectedRuns:    reproductionContractRuns("0123456789abcdef0123456789abcdef01234567"),
		DiagnosisBaseRuns: reproductionContractRuns(
			"89abcdef0123456789abcdef0123456789abcdef",
		),
		ArtifactRunID: 5, ArtifactName: "reproduction",
		ArtifactSHA256: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	}
	checkpoint.Work = &issueagent.Work{
		Branch:  "agent/issue-42",
		HeadSHA: "76543210fedcba9876543210fedcba9876543210",
	}
	return checkpoint
}

func successorBaseCheckpoint() issueagent.Checkpoint {
	return issueagent.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		State: issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion: "v2.0.0", AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42", AuthorizedBy: "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			AffectedSHA:      "0123456789abcdef0123456789abcdef01234567",
			DiagnosisBaseSHA: "89abcdef0123456789abcdef0123456789abcdef",
		},
		NextAction: issueagent.ActionPinVersions,
	}
}

func reproductionContractRuns(sha string) []issueagent.ReproductionRun {
	runs := make([]issueagent.ReproductionRun, 3)
	for index := range runs {
		runs[index] = issueagent.ReproductionRun{
			RunID: int64(index + 1), SourceSHA: sha,
			BinarySHA256:    "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			CommandSHA256:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			AssertionSHA256: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			Outcome:         "assertion_failed",
		}
	}
	return runs
}

func cloneReproduction(input *issueagent.Reproduction) *issueagent.Reproduction {
	output := *input
	output.TestFiles = append([]issueagent.TestFile(nil), input.TestFiles...)
	output.AffectedRuns = append([]issueagent.ReproductionRun(nil), input.AffectedRuns...)
	output.DiagnosisBaseRuns = append(
		[]issueagent.ReproductionRun(nil), input.DiagnosisBaseRuns...,
	)
	return &output
}
