package issueagent_test

import (
	"testing"

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
