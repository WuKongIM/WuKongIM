package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestParseCommandAcceptsExactAuthorizedFirstLine(t *testing.T) {
	t.Parallel()

	actor := issueagentusecase.CommandActor{
		Login:      "maintainer",
		Type:       "User",
		Permission: issueagentusecase.PermissionMaintain,
	}
	policy := issueagentusecase.CommandPolicy{
		AllowedBackportBranches: []string{"release-2.0", "release-2.1"},
	}

	tests := []struct {
		text string
		kind issueagentusecase.CommandKind
	}{
		{text: "/agent revise\nUse the updated reproduction.", kind: issueagentusecase.CommandRevise},
		{text: "/agent cancel", kind: issueagentusecase.CommandCancel},
		{text: "/agent address-review", kind: issueagentusecase.CommandAddressReview},
		{text: "/agent approve-risk", kind: issueagentusecase.CommandApproveRisk},
		{
			text: "/agent adopt-head 0123456789abcdef0123456789abcdef01234567",
			kind: issueagentusecase.CommandAdoptHead,
		},
		{text: "/agent backport release-2.0", kind: issueagentusecase.CommandBackport},
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.kind), func(t *testing.T) {
			t.Parallel()
			intent, err := issueagentusecase.ParseCommand(test.text, actor, policy)
			require.NoError(t, err)
			require.Equal(t, test.kind, intent.Kind)
		})
	}
}

func TestPlanCommandRevisesFrozenInputAndFencesOldGeneration(t *testing.T) {
	t.Parallel()

	current := commandCheckpoint()
	intent := issueagentusecase.CommandIntent{
		Kind: issueagentusecase.CommandRevise, Actor: "maintainer",
	}
	plan, err := issueagentusecase.PlanCommand(
		current, intent, issueagentusecase.CommandFacts{
			IssueBodySHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			AffectedVersion: "v2.1.0", DiagnosisBaseSHA: baseSHA,
			CommandEventID: "comment-501", CurrentCommentID: 100,
			CurrentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), plan.PreviousGeneration)
	require.Equal(t, uint64(2), plan.NewGeneration)
	require.NotNil(t, plan.RevisedCheckpoint)
	require.Equal(t, "v2.1.0", plan.RevisedCheckpoint.FrozenInput.AffectedVersion)
	require.Equal(t, current.FrozenInput.AffectedVersion, "v2.0.0")
}

func TestPlanCommandFreezesReviewThreadsAndExactAdoptedHead(t *testing.T) {
	t.Parallel()

	current := commandCheckpoint()
	current.Work = &issueagent.Work{
		Branch: "agent/issue-42", HeadSHA: affectedSHA, PRNumber: 9,
	}
	review, err := issueagentusecase.PlanCommand(
		current,
		issueagentusecase.CommandIntent{
			Kind: issueagentusecase.CommandAddressReview, Actor: "maintainer",
		},
		issueagentusecase.CommandFacts{
			CommandEventID:      "review-command",
			UnresolvedThreadIDs: []string{"PRRT_1", "PRRT_2"},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"PRRT_1", "PRRT_2"}, review.ReviewThreadIDs)

	external := "234567890abcdef1234567890abcdef123456789"
	_, err = issueagentusecase.PlanCommand(
		current,
		issueagentusecase.CommandIntent{
			Kind: issueagentusecase.CommandAdoptHead, Actor: "maintainer",
			HeadSHA: external,
		},
		issueagentusecase.CommandFacts{
			CommandEventID: "adopt", CurrentExternalHead: affectedSHA,
		},
	)
	require.Error(t, err)
	adopted, err := issueagentusecase.PlanCommand(
		current,
		issueagentusecase.CommandIntent{
			Kind: issueagentusecase.CommandAdoptHead, Actor: "maintainer",
			HeadSHA: external,
		},
		issueagentusecase.CommandFacts{
			CommandEventID: "adopt", CurrentExternalHead: external,
		},
	)
	require.NoError(t, err)
	require.Equal(t, external, adopted.AdoptedHeadSHA)
}

func TestPlanCommandIsolatesBackportAndAuditedRecovery(t *testing.T) {
	t.Parallel()

	merged := commandCheckpoint()
	merged.State = issueagent.StateMerged
	merged.NextAction = issueagent.ActionNone
	backport, err := issueagentusecase.PlanCommand(
		merged,
		issueagentusecase.CommandIntent{
			Kind: issueagentusecase.CommandBackport, Actor: "maintainer",
			BackportBranch: "release-2.0",
		},
		issueagentusecase.CommandFacts{
			CommandEventID: "backport", MergedPRNumber: 9,
			TargetBranch: "release-2.0", TargetHeadSHA: baseSHA,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, backport.Backport)
	require.Equal(t, int64(42), backport.Backport.SourceIssue)
	require.Equal(t, baseSHA, backport.Backport.TargetHeadSHA)

	recoveryDigest := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	recovery, err := issueagentusecase.PlanCommand(
		commandCheckpoint(),
		issueagentusecase.CommandIntent{
			Kind: issueagentusecase.CommandRecoverChain, Actor: "admin",
			CheckpointCommentID: 100, CheckpointDigest: recoveryDigest,
		},
		issueagentusecase.CommandFacts{
			CommandEventID: "recover", LastValidCommentID: 100,
			LastValidDigest:       recoveryDigest,
			QuarantinedCommentIDs: []int64{101, 102},
			QuarantineDigest:      "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, recovery.Recovery)
	require.Equal(t, []int64{101, 102}, recovery.Recovery.QuarantinedCommentIDs)
}

func commandCheckpoint() issueagent.Checkpoint {
	return issueagent.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		State: issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion: "v2.0.0", AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "label-event", AuthorizedBy: "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef: "v2.0.0", DiagnosisBaseSHA: baseSHA,
		},
		NextAction: issueagent.ActionPinVersions,
	}
}

func TestParseCommandRejectsQuotedEmbeddedAndUnauthorizedText(t *testing.T) {
	t.Parallel()

	policy := issueagentusecase.CommandPolicy{
		AllowedBackportBranches: []string{"release-2.0"},
	}
	maintainer := issueagentusecase.CommandActor{
		Login:      "maintainer",
		Type:       "User",
		Permission: issueagentusecase.PermissionWrite,
	}

	tests := []struct {
		name  string
		text  string
		actor issueagentusecase.CommandActor
	}{
		{name: "quoted", text: "> /agent cancel", actor: maintainer},
		{name: "embedded", text: "please do this\n/agent cancel", actor: maintainer},
		{name: "fenced", text: "```\n/agent cancel\n```", actor: maintainer},
		{
			name: "public",
			text: "/agent cancel",
			actor: issueagentusecase.CommandActor{
				Login: "reporter", Type: "User", Permission: issueagentusecase.PermissionRead,
			},
		},
		{
			name: "model bot",
			text: "/agent cancel",
			actor: issueagentusecase.CommandActor{
				Login: "agent[bot]", Type: "Bot", Permission: issueagentusecase.PermissionWrite,
			},
		},
		{name: "unapproved branch", text: "/agent backport main", actor: maintainer},
		{name: "extra argument", text: "/agent revise now", actor: maintainer},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := issueagentusecase.ParseCommand(test.text, test.actor, policy)
			require.Error(t, err)
		})
	}
}

func TestParseCommandRequiresAdminForAuditChainRecovery(t *testing.T) {
	t.Parallel()

	text := "/agent recover-chain 123 sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	policy := issueagentusecase.CommandPolicy{}

	_, err := issueagentusecase.ParseCommand(text, issueagentusecase.CommandActor{
		Login: "maintainer", Type: "User", Permission: issueagentusecase.PermissionMaintain,
	}, policy)
	require.Error(t, err)

	intent, err := issueagentusecase.ParseCommand(text, issueagentusecase.CommandActor{
		Login: "admin", Type: "User", Permission: issueagentusecase.PermissionAdmin,
	}, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.CommandRecoverChain, intent.Kind)
	require.Equal(t, int64(123), intent.CheckpointCommentID)
}
