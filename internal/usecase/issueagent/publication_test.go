package issueagent_test

import (
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestPlanCandidatePublicationRequiresExactTrustedEvidence(t *testing.T) {
	t.Parallel()

	input := validPublicationInput(t)
	plan, err := issueagent.PlanCandidatePublication(input)
	require.NoError(t, err)
	require.Equal(t, "agent/issue-42", plan.Branch)
	require.Equal(t, "fix(agent): resolve issue #42", plan.CommitMessage)
	require.Contains(t, plan.PullRequestBody, "Fixes #42")
	require.Contains(t, plan.PullRequestBody, "## Trusted verification")

	forged := input
	forged.Candidate.ChangeSet.Files[0].ContentBase64 =
		contract.EncodeFileContent([]byte("package example\n\nfunc forged() {}\n"))
	_, err = issueagent.PlanCandidatePublication(forged)
	require.EqualError(t, err, "candidate publication digest does not match signed state")
}

func TestPlanCandidatePublicationRejectsAdvisoryReadyWithoutVerifierApproval(
	t *testing.T,
) {
	t.Parallel()

	input := validPublicationInput(t)
	input.Evidence.PublicationEligible = false
	input.Evidence.Risk = contract.CandidateRiskHigh
	input.Evidence.FailureReason = "protected path"
	input.Evidence.Commands = []contract.VerificationCommand{}
	_, err := issueagent.PlanCandidatePublication(input)
	require.EqualError(t, err, "Verifier rejected candidate publication")
}

func TestBuildPublishedStateRecordsExactDraft(t *testing.T) {
	t.Parallel()

	input := validPublicationInput(t)
	next, err := issueagent.BuildPublishedState(
		input.State,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		9,
		input.State.CandidateDigest,
		input.State.EvidenceDigest,
		time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, contract.IssueStateDraft, next.State)
	require.Nil(t, next.Task)
	require.Equal(t, int64(9), next.Work.PullRequest)
	require.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", next.Work.HeadSHA)
	require.True(t, next.Work.Draft)
}

func TestBuildPublishedStatePreservesReadyReviewPullRequest(t *testing.T) {
	t.Parallel()

	input := validPublicationInput(t)
	input.State.State = contract.IssueStateReviewing
	input.State.Task.Kind = contract.TaskKindReview
	input.State.ReviewDigest =
		"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	input.State.Work = &contract.IssueWork{
		Branch:      "agent/issue-42",
		HeadSHA:     input.State.Task.BaseSHA,
		PullRequest: 9,
		Draft:       false,
	}
	next, err := issueagent.BuildPublishedState(
		input.State,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		9,
		input.State.CandidateDigest,
		input.State.EvidenceDigest,
		time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, contract.IssueStateReadyForReview, next.State)
	require.False(t, next.Work.Draft)
	require.Equal(t, int64(9), next.Work.PullRequest)
	require.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", next.Work.HeadSHA)
}

func validPublicationInput(t *testing.T) issueagent.CandidatePublicationInput {
	t.Helper()

	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	task := contract.TaskIdentity{
		ID:           "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Kind:         contract.TaskKindEngineer,
		BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
		PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	context := contract.ContextBundle{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 1, Task: task,
		Trusted: contract.TrustedContext{
			Authorization: contract.AuthorizationRecord{
				Actor: "maintainer", Permission: "write",
				EventID: "issue-42", Command: "/agent fix",
			},
			Labels: []string{"bug"}, RequiredTests: []string{"focused"},
			RiskCeiling: []string{"low"},
			InstructionDigests: []contract.FileDigest{{
				Path:       "AGENTS.md",
				GitBlobSHA: "dddddddddddddddddddddddddddddddddddddddd",
			}},
			KnowledgePaths:     []string{"docs/development/PROJECT_KNOWLEDGE.md"},
			OutputSchemaDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Limits: contract.EngineerLimits{
				WallTimeSeconds: 5400, ModifyTestIterations: 3,
			},
		},
		Untrusted: contract.UntrustedContext{
			Issue: contract.IssueSnapshot{
				ID: "I_42", Number: 42, Title: "broken", Body: "steps",
				Author: "reporter", AuthorAssociation: "NONE", UpdatedAt: now,
			},
			Comments:      []contract.CommentSnapshot{},
			ReviewThreads: []contract.ReviewThreadSnapshot{},
		},
		CreatedAt: now,
	}
	candidate := contract.CandidateSnapshot{
		SchemaVersion: 2, TaskID: task.ID, BaseSHA: task.BaseSHA,
		ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
			Path:      "internal/example/fix.go",
			Operation: contract.FileOperationUpsert,
			Mode:      contract.FileModeRegular,
			ContentBase64: contract.EncodeFileContent(
				[]byte("package example\n\nfunc fixed() {}\n"),
			),
		}}},
	}
	candidateDigest, err := contract.CandidateSnapshotDigest(candidate)
	require.NoError(t, err)
	changeDigest, err := contract.ChangeSetDigest(candidate.ChangeSet)
	require.NoError(t, err)
	evidence := contract.CandidateEvidence{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, TaskID: task.ID, BaseSHA: task.BaseSHA,
		CandidateDigest: candidateDigest, ChangeSetDigest: changeDigest,
		Risk: contract.CandidateRiskLow, PublicationEligible: true,
		RequiredSuites: []string{"focused"},
		Commands: []contract.VerificationCommand{{
			Arguments:  []string{"go", "test", "./internal/example"},
			WorkingDir: ".", ExitCode: 0,
			StdoutDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			StderrDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			DurationMS:   10,
		}},
		CreatedAt: now,
	}
	evidenceDigest, err := contract.CandidateEvidenceDigest(evidence)
	require.NoError(t, err)
	contextDigest, err := contract.ContextBundleDigest(context)
	require.NoError(t, err)
	state := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 2,
		State:               contract.IssueStateEngineering,
		Reason:              "candidate captured",
		PreviousStateDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		IssueSnapshotDigest: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		SourceSHA:           task.BaseSHA, Task: &task,
		Authorization:   &context.Trusted.Authorization,
		ContextDigest:   contextDigest,
		CandidateDigest: candidateDigest,
		EvidenceDigest:  evidenceDigest,
		UpdatedAt:       now,
	}
	return issueagent.CandidatePublicationInput{
		State: state, Context: context,
		Engineer: contract.EngineerResult{
			SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
			IssueNumber: 42, TaskID: task.ID,
			Outcome:         contract.EngineerOutcomeReady,
			ExternalSymptom: "request fails", RootCause: "wrong condition",
			CausalPath:         "request -> condition -> failure",
			EvidenceReferences: []string{"focused regression test"},
			ProposedRisk:       []string{"narrow behavior change"},
			TestsAttempted:     []string{"go test ./internal/example"},
			Summary:            "Correct the condition and add a regression test.",
			Ready:              true,
		},
		Candidate: candidate, Evidence: evidence,
		ExpectedParentSHA: task.BaseSHA,
		BaseTreeSHA:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}
