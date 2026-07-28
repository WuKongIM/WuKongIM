package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeFreezesOnlyFreshMaintainerLabelEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	checkpoint, err := issueagentusecase.Authorize(
		issueagentusecase.AuthorizationFacts{
			Repository:          "WuKongIM/WuKongIM",
			IssueNumber:         42,
			EventID:             "evt-42",
			EventAction:         "labeled",
			Label:               "ready-for-agent",
			BeforeLabels:        []string{"needs-triage"},
			AfterLabels:         []string{"ready-for-agent"},
			Actor:               "maintainer",
			ActorType:           "User",
			Permission:          issueagentusecase.PermissionWrite,
			EventAt:             now.Add(-2 * time.Minute),
			PermissionCheckedAt: now.Add(-time.Minute),
			IssueBodySHA256:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion:     "v2.0.0",
			AcceptedCommentIDs:  []int64{101, 102},
			DiagnosisBaseSHA:    "0123456789abcdef0123456789abcdef01234567",
		},
		now,
		5*time.Minute,
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.StateAuthorized, checkpoint.State)
	require.Equal(t, issueagent.ActionPinVersions, checkpoint.NextAction)
	require.Equal(t, uint64(1), checkpoint.Generation)
	require.Equal(t, uint64(1), checkpoint.Sequence)
	require.Equal(t, "maintainer", checkpoint.FrozenInput.AuthorizedBy)
}

func TestAuthorizeRejectsPublicStaleAndPreexistingLabels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	base := issueagentusecase.AuthorizationFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		EventID:             "evt-42",
		EventAction:         "labeled",
		Label:               "ready-for-agent",
		BeforeLabels:        []string{"needs-triage"},
		AfterLabels:         []string{"ready-for-agent"},
		Actor:               "maintainer",
		ActorType:           "User",
		Permission:          issueagentusecase.PermissionWrite,
		EventAt:             now.Add(-2 * time.Minute),
		PermissionCheckedAt: now.Add(-time.Minute),
		IssueBodySHA256:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AffectedVersion:     "v2.0.0",
		DiagnosisBaseSHA:    "0123456789abcdef0123456789abcdef01234567",
	}

	tests := []struct {
		name   string
		mutate func(*issueagentusecase.AuthorizationFacts)
	}{
		{
			name: "public actor",
			mutate: func(facts *issueagentusecase.AuthorizationFacts) {
				facts.Permission = issueagentusecase.PermissionRead
			},
		},
		{
			name: "stale permission",
			mutate: func(facts *issueagentusecase.AuthorizationFacts) {
				facts.PermissionCheckedAt = now.Add(-10 * time.Minute)
			},
		},
		{
			name: "preexisting label",
			mutate: func(facts *issueagentusecase.AuthorizationFacts) {
				facts.BeforeLabels = []string{"ready-for-agent"}
			},
		},
		{
			name: "bot actor",
			mutate: func(facts *issueagentusecase.AuthorizationFacts) {
				facts.ActorType = "Bot"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := base
			test.mutate(&facts)
			_, err := issueagentusecase.Authorize(facts, now, 5*time.Minute)
			require.Error(t, err)
		})
	}
}
