package issueagent_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestDecodeCheckpointEnvelopeRejectsUnknownTrailingAndOversizedJSON(t *testing.T) {
	t.Parallel()

	envelope := issueagent.CheckpointEnvelope{
		SchemaVersion: 1,
		KeyID:         "checkpoint-2026-07",
		Checkpoint: issueagent.Checkpoint{
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
				DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
			},
			Budget:     issueagent.Budget{},
			NextAction: issueagent.ActionPinVersions,
		},
		Signature: base64.RawStdEncoding.EncodeToString(make([]byte, 64)),
	}
	body, err := json.Marshal(envelope)
	require.NoError(t, err)

	decoded, err := issueagent.DecodeCheckpointEnvelope(
		bytes.NewReader(body), int64(len(body)),
	)
	require.NoError(t, err)
	require.Equal(t, envelope, decoded)

	withUnknown := bytes.Replace(
		body,
		[]byte(`"schema_version":1`),
		[]byte(`"schema_version":1,"unexpected":true`),
		1,
	)
	_, err = issueagent.DecodeCheckpointEnvelope(
		bytes.NewReader(withUnknown), int64(len(withUnknown)),
	)
	require.Error(t, err)

	_, err = issueagent.DecodeCheckpointEnvelope(
		strings.NewReader(string(body)+"{}"), int64(len(body)+2),
	)
	require.Error(t, err)

	_, err = issueagent.DecodeCheckpointEnvelope(
		bytes.NewReader(body), int64(len(body)-1),
	)
	require.Error(t, err)
}
