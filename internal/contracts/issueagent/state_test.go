package issueagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestIssueAgentStateCanonicalBytesBindInitialTriage(t *testing.T) {
	t.Parallel()

	state := issueagent.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            1,
		State:               issueagent.IssueStateTriaging,
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		UpdatedAt:           time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}

	got, err := issueagent.CanonicalIssueAgentState(state)
	require.NoError(t, err)
	require.Equal(t,
		`{"schema_version":2,"repository":"WuKongIM/WuKongIM","issue_number":42,"sequence":1,"state":"triaging","reason":"","previous_state_digest":"","issue_snapshot_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source_sha":"0123456789abcdef0123456789abcdef01234567","task":null,"authorization":null,"budget":{"engineer_attempts":0,"review_iterations":0},"work":null,"status_comment_id":0,"context_digest":"","candidate_digest":"","evidence_digest":"","review_digest":"","taken_over_by":"","updated_at":"2026-07-30T01:02:03Z"}`,
		string(got),
	)
}

func TestDecodeIssueAgentStateRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := issueagent.DecodeIssueAgentState(strings.NewReader(
		`{"schema_version":2,"repository":"WuKongIM/WuKongIM",`+
			`"issue_number":42,"sequence":1,`+
			`"state":"triaging","reason":"","previous_state_digest":"",`+
			`"issue_snapshot_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"source_sha":"0123456789abcdef0123456789abcdef01234567",`+
			`"task":null,"budget":{"engineer_attempts":0,`+
			`"review_iterations":0},"work":null,`+
			`"status_comment_id":0,"context_digest":"",`+
			`"candidate_digest":"","evidence_digest":"","review_digest":"",`+
			`"taken_over_by":"","updated_at":"2026-07-30T01:02:03Z",`+
			`"injected":"do not validate"}`),
		16<<10,
	)
	require.EqualError(t, err, `decode JSON input: json: unknown field "injected"`)
}
