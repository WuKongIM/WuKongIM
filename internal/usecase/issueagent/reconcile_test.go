package issueagent_test

import (
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

const (
	publishedContextDigest   = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	publishedCandidateDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	publishedEvidenceDigest  = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func TestReconcileIssueWaitsForAuthorizationFromExternalReporter(t *testing.T) {
	t.Parallel()

	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "CONTRIBUTOR",
		AuthorPermission:    "read",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
	}, nil, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionWaitAuthorization, decision.Kind)
	require.Equal(t, contract.IssueStateWaitingForAuthorization, decision.NextState)
	require.Nil(t, decision.Task)
}

func TestReconcileIssueDispatchesOneDeterministicTaskForTrustedBug(t *testing.T) {
	t.Parallel()

	facts := issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "1234567890abcdef1234567890abcdef12345678",
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
	}
	policy := issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)

	first, err := issueagent.ReconcileIssue(facts, nil, policy, now)
	require.NoError(t, err)
	second, err := issueagent.ReconcileIssue(facts, nil, policy, now)
	require.NoError(t, err)

	require.Equal(t, issueagent.IssueDecisionDispatchEngineer, first.Kind)
	require.Equal(t, contract.IssueStateEngineering, first.NextState)
	require.NotNil(t, first.Task)
	require.Equal(t, contract.TaskKindEngineer, first.Task.Kind)
	require.Equal(t, first.Task, second.Task)
}

func TestReconcileIssueAcceptsFreshMaintainerFixAuthorization(t *testing.T) {
	t.Parallel()

	authorization := contract.AuthorizationRecord{
		Actor:      "maintainer",
		Permission: "maintain",
		EventID:    "issue_comment:9001",
		Command:    "/agent fix",
	}
	facts := issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "CONTRIBUTOR",
		AuthorPermission:    "read",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		Authorization:       &authorization,
	}

	decision, err := issueagent.ReconcileIssue(facts, nil,
		issueagent.ReconcileIssuePolicy{
			Enabled:              true,
			PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			MaxEngineerAttempts:  3,
			MaxReviewIterations:  2,
		},
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionDispatchEngineer, decision.Kind)
}

func TestReconcileIssueRequestsMissingInformationBeforeEngineering(t *testing.T) {
	t.Parallel()

	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: false,
		Risk:                contract.CandidateRiskLow,
	}, nil, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionRequestInformation, decision.Kind)
	require.Equal(t, contract.IssueStateWaitingForInformation, decision.NextState)
	require.Nil(t, decision.Task)
}

func TestReconcileIssueExplainsInvalidAffectedVersion(t *testing.T) {
	t.Parallel()

	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: false,
		MissingInformation:  "Affected version must be an existing release tag or full commit SHA.",
		Risk:                contract.CandidateRiskLow,
	}, nil, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionRequestInformation, decision.Kind)
	require.Equal(t,
		"Affected version must be an existing release tag or full commit SHA.",
		decision.Reason,
	)
}

func TestReconcileIssueHandsHighRiskWorkToHuman(t *testing.T) {
	t.Parallel()

	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "OWNER",
		AuthorPermission:    "admin",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: true,
		Risk:                contract.CandidateRiskHigh,
	}, nil, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionNeedsHuman, decision.Kind)
	require.Equal(t, contract.IssueStateNeedsHuman, decision.NextState)
	require.Nil(t, decision.Task)
}

func TestReconcileIssueGroupsTrustedReviewIntoFreshTask(t *testing.T) {
	t.Parallel()

	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            4,
		State:               contract.IssueStateDraft,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch:      "agent/issue-42",
			HeadSHA:     "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84,
			Draft:       true,
		},
		ContextDigest:   publishedContextDigest,
		CandidateDigest: publishedCandidateDigest,
		EvidenceDigest:  publishedEvidenceDigest,
		UpdatedAt:       time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		ReviewDigest:        "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Authorization: &contract.AuthorizationRecord{
			Actor:      "wukongim-review-agent[bot]",
			Permission: "review_agent",
			EventID:    "pull_request_review:99",
		},
		PullRequest: &issueagent.PullRequestFacts{
			Number: 84, HeadSHA: current.Work.HeadSHA,
			Open: true, Draft: false,
		},
	}, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionDispatchReview, decision.Kind)
	require.Equal(t, contract.IssueStateReviewing, decision.NextState)
	require.NotNil(t, decision.Task)
	require.Equal(t, contract.TaskKindReview, decision.Task.Kind)
	require.Equal(t, current.Work.HeadSHA, decision.Task.BaseSHA)
	next, err := issueagent.BuildIssueState(
		&current,
		issueagent.IssueSnapshotFacts{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			IssueSnapshotDigest: current.IssueSnapshotDigest,
			SourceSHA:           current.SourceSHA,
			ReviewDigest:        "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Authorization: &contract.AuthorizationRecord{
				Actor:      "wukongim-review-agent[bot]",
				Permission: "review_agent",
				EventID:    "pull_request_review:99",
			},
			PullRequest: &issueagent.PullRequestFacts{
				Number: 84, HeadSHA: current.Work.HeadSHA,
				Open: true, Draft: false,
			},
		},
		decision,
		time.Date(2026, 7, 30, 1, 2, 4, 0, time.UTC),
	)
	require.NoError(t, err)
	require.False(t, next.Work.Draft)
	require.Equal(t,
		"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		next.ReviewDigest,
	)
}

func TestReconcileIssueDoesNotDuplicateActiveEngineeringTask(t *testing.T) {
	t.Parallel()

	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            2,
		State:               contract.IssueStateEngineering,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Task: &contract.TaskIdentity{
			ID:           "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Kind:         contract.TaskKindEngineer,
			BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
			AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
			PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
		Authorization: &contract.AuthorizationRecord{
			Actor: "maintainer", Permission: "write",
			EventID: "issue:42", Command: "/agent fix",
		},
		UpdatedAt: time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
	}, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionWait, decision.Kind)
	require.Equal(t, contract.IssueStateEngineering, decision.NextState)
	require.Nil(t, decision.Task)
}

func TestReconcileIssueStopsStaleActiveTask(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 2,
		State:               contract.IssueStateEngineering,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Task: &contract.TaskIdentity{
			ID:           "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Kind:         contract.TaskKindEngineer,
			BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
			AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
			PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
		Authorization: &contract.AuthorizationRecord{
			Actor: "maintainer", Permission: "write",
			EventID: "issue:42", Command: "/agent fix",
		},
		UpdatedAt: updatedAt,
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42, Open: true,
		AuthorAssociation: "MEMBER", AuthorPermission: "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
	}, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3, MaxReviewIterations: 2,
		TaskStaleAfter: 4 * time.Hour,
	}, updatedAt.Add(4*time.Hour))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionNeedsHuman, decision.Kind)
	require.Equal(t, contract.IssueStateNeedsHuman, decision.NextState)
	require.Contains(t, decision.Reason, "terminal Publisher result")
}

func TestReconcileIssueStopsWritesAfterMaintainerTakeOver(t *testing.T) {
	t.Parallel()

	control := contract.AuthorizationRecord{
		Actor:      "maintainer",
		Permission: "write",
		EventID:    "issue_comment:9002",
		Command:    "/agent take-over",
	}
	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            4,
		State:               contract.IssueStateDraft,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch:      "agent/issue-42",
			HeadSHA:     "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84,
			Draft:       true,
		},
		ContextDigest:   publishedContextDigest,
		CandidateDigest: publishedCandidateDigest,
		EvidenceDigest:  publishedEvidenceDigest,
		UpdatedAt:       time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		Authorization:       &control,
	}, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionTakeOver, decision.Kind)
	require.Equal(t, contract.IssueStateTakenOver, decision.NextState)
	require.Nil(t, decision.Task)
}

func TestReconcileIssueRetryCreatesFreshTaskFromNeedsHuman(t *testing.T) {
	t.Parallel()

	retry := contract.AuthorizationRecord{
		Actor:      "maintainer",
		Permission: "admin",
		EventID:    "issue_comment:9003",
		Command:    "/agent retry",
	}
	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            5,
		State:               contract.IssueStateNeedsHuman,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Budget: contract.IssueBudget{
			EngineerAttempts: 1,
		},
		UpdatedAt: time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	facts := issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "CONTRIBUTOR",
		AuthorPermission:    "read",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		Authorization:       &retry,
	}
	policy := issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}
	decision, err := issueagent.ReconcileIssue(facts, &current, policy,
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionDispatchEngineer, decision.Kind)
	require.Equal(t, contract.IssueStateEngineering, decision.NextState)
	require.NotNil(t, decision.Task)
	require.NotEqual(t, current.Task, decision.Task)
}

func TestReconcileIssueIgnoresRetryOutsideNeedsHuman(t *testing.T) {
	t.Parallel()

	retry := contract.AuthorizationRecord{
		Actor:      "maintainer",
		Permission: "admin",
		EventID:    "issue_comment:9003",
		Command:    "/agent retry",
	}
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 5,
		State:               contract.IssueStateDraft,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch: "agent/issue-42", HeadSHA: "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84, Draft: true,
		},
		ContextDigest: publishedContextDigest, CandidateDigest: publishedCandidateDigest,
		EvidenceDigest: publishedEvidenceDigest,
		UpdatedAt:      time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42, Open: true,
		AuthorAssociation: "CONTRIBUTOR", AuthorPermission: "read",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA, AffectedSHA: current.SourceSHA,
		InformationComplete: true, Risk: contract.CandidateRiskLow,
		Authorization: &retry,
		PullRequest: &issueagent.PullRequestFacts{
			Number: 84, HeadSHA: current.Work.HeadSHA, Open: true, Draft: true,
		},
	}, &current, testReconcilePolicy(),
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionWait, decision.Kind)
}

func TestReconcileIssueRetryResumesFailedReviewFromPublishedHead(t *testing.T) {
	t.Parallel()

	reviewDigest := "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	retry := contract.AuthorizationRecord{
		Actor: "maintainer", Permission: "admin",
		EventID: "issue_comment:9004", Command: "/agent retry",
	}
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 7,
		State:               contract.IssueStateNeedsHuman,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch: "agent/issue-42", HeadSHA: "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84, Draft: true,
		},
		ReviewDigest: reviewDigest,
		Budget:       contract.IssueBudget{EngineerAttempts: 1, ReviewIterations: 1},
		UpdatedAt:    time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42, Open: true,
		AuthorAssociation: "CONTRIBUTOR", AuthorPermission: "read",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA, AffectedSHA: current.SourceSHA,
		InformationComplete: true, Risk: contract.CandidateRiskLow,
		Authorization: &retry,
		PullRequest: &issueagent.PullRequestFacts{
			Number: 84, HeadSHA: current.Work.HeadSHA, Open: true, Draft: true,
		},
	}, &current, testReconcilePolicy(),
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionDispatchReview, decision.Kind)
	require.Equal(t, contract.TaskKindReview, decision.Task.Kind)
	require.Equal(t, current.Work.HeadSHA, decision.Task.BaseSHA)
}

func testReconcilePolicy() issueagent.ReconcileIssuePolicy {
	return issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3, MaxReviewIterations: 2,
	}
}

func TestReconcileIssueFollowsMaintainerReadyTransition(t *testing.T) {
	t.Parallel()

	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            5,
		State:               contract.IssueStateDraft,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch:      "agent/issue-42",
			HeadSHA:     "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84,
			Draft:       true,
		},
		ContextDigest:   publishedContextDigest,
		CandidateDigest: publishedCandidateDigest,
		EvidenceDigest:  publishedEvidenceDigest,
		UpdatedAt:       time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	facts := issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                true,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		PullRequest: &issueagent.PullRequestFacts{
			Number: 84, HeadSHA: current.Work.HeadSHA, Open: true, Draft: false,
		},
	}
	decision, err := issueagent.ReconcileIssue(facts, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionMarkReady, decision.Kind)
	require.Equal(t, contract.IssueStateReadyForReview, decision.NextState)
	next, err := issueagent.BuildIssueState(
		&current,
		facts,
		decision,
		time.Date(2026, 7, 30, 1, 2, 4, 0, time.UTC),
	)
	require.NoError(t, err)
	require.False(t, next.Work.Draft)
}

func TestReconcileIssueCompletesAfterHumanMerge(t *testing.T) {
	t.Parallel()

	current := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            6,
		State:               contract.IssueStateReadyForReview,
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch:      "agent/issue-42",
			HeadSHA:     "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84,
			Draft:       false,
		},
		ContextDigest:   publishedContextDigest,
		CandidateDigest: publishedCandidateDigest,
		EvidenceDigest:  publishedEvidenceDigest,
		UpdatedAt:       time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                false,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: current.IssueSnapshotDigest,
		SourceSHA:           current.SourceSHA,
		AffectedSHA:         current.SourceSHA,
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
		PullRequest: &issueagent.PullRequestFacts{
			Number: 84, HeadSHA: current.Work.HeadSHA, Merged: true,
		},
	}, &current, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionComplete, decision.Kind)
	require.Equal(t, contract.IssueStateCompleted, decision.NextState)
}

func TestReconcileIssueCancelsClosedIssueWithoutMergedAgentWork(t *testing.T) {
	t.Parallel()

	decision, err := issueagent.ReconcileIssue(issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Open:                false,
		AuthorAssociation:   "MEMBER",
		AuthorPermission:    "write",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:         "0123456789abcdef0123456789abcdef01234567",
		InformationComplete: true,
		Risk:                contract.CandidateRiskLow,
	}, nil, issueagent.ReconcileIssuePolicy{
		Enabled:              true,
		PolicyDigest:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EngineerPromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ReviewPromptDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		MaxEngineerAttempts:  3,
		MaxReviewIterations:  2,
	}, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, issueagent.IssueDecisionCancel, decision.Kind)
	require.Equal(t, contract.IssueStateCancelled, decision.NextState)
}
