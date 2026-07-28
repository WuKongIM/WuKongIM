package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCheckpointCanonicalBytesBindFirstAuthorizedSnapshot(t *testing.T) {
	t.Parallel()

	diagnosisBase := "0123456789abcdef0123456789abcdef01234567"
	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      1,
		State:         issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion:    "v2.0.0",
			AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42",
			AuthorizedBy:       "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			DiagnosisBaseSHA: diagnosisBase,
		},
		Budget:     issueagent.Budget{},
		NextAction: issueagent.ActionPinVersions,
	}

	require.NoError(t, issueagent.ValidateCheckpoint(checkpoint))

	got, err := issueagent.CanonicalCheckpoint(checkpoint)
	require.NoError(t, err)
	require.Equal(t,
		`{"schema_version":1,"repository":"WuKongIM/WuKongIM","issue_number":42,"generation":1,"sequence":1,"expected_previous_checkpoint_id":null,"previous_checkpoint_sha256":null,"state":"authorized","frozen_input":{"issue_body_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","affected_version":"v2.0.0","accepted_comment_ids":[],"authorization_event":"evt-42","authorized_by":"maintainer"},"versions":{"reported_ref":"v2.0.0","affected_sha":"","diagnosis_base_sha":"0123456789abcdef0123456789abcdef01234567","integration_base_sha":null},"lease":null,"reproduction":null,"work":null,"diagnosis":null,"validation":null,"budget":{"reproduction_attempts":0,"remediation_attempts":0,"ci_repair_attempts":0,"infrastructure_attempts":0,"worker_seconds":0},"model":null,"next_action":"pin_versions"}`,
		string(got),
	)
}

func TestCheckpointRejectsMalformedIdentityBeforeSigning(t *testing.T) {
	t.Parallel()

	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "../other",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      1,
		State:         issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256:    "sha256:not-a-digest",
			AffectedVersion:    "latest",
			AcceptedCommentIDs: []int64{3, 2},
			AuthorizationEvent: "evt-42",
			AuthorizedBy:       "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "latest",
			DiagnosisBaseSHA: "main",
		},
		NextAction: issueagent.ActionPinVersions,
	}

	_, err := issueagent.CanonicalCheckpoint(checkpoint)
	require.Error(t, err)
}
