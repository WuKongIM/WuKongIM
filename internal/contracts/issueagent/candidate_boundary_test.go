package issueagent_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCandidateSnapshotStrictRoundTripBindsCapturedFileBytes(t *testing.T) {
	t.Parallel()

	want := issueagent.CandidateSnapshot{
		SchemaVersion: 2,
		TaskID:        issueAgentDigest("a"),
		BaseSHA:       issueAgentSHA("1"),
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:          "internal/runtime/example.go",
			Operation:     issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: issueagent.EncodeFileContent([]byte("package runtime\n")),
		}}},
	}
	body, err := json.Marshal(want)
	require.NoError(t, err)
	got, err := issueagent.DecodeCandidateSnapshot(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, want, got)

	digest, err := issueagent.ChangeSetDigest(got.ChangeSet)
	require.NoError(t, err)
	got.ChangeSet.Files[0].ContentBase64 = issueagent.EncodeFileContent([]byte("package changed\n"))
	changed, err := issueagent.ChangeSetDigest(got.ChangeSet)
	require.NoError(t, err)
	require.NotEqual(t, digest, changed)
}

func TestCandidateSnapshotRejectsMalformedIdentityAndContent(t *testing.T) {
	t.Parallel()

	snapshot := issueagent.CandidateSnapshot{
		SchemaVersion: 2,
		TaskID:        issueAgentDigest("a"),
		BaseSHA:       issueAgentSHA("1"),
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:          "fix.go",
			Operation:     issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: "not canonical base64",
		}}},
	}
	require.Error(t, issueagent.ValidateCandidateSnapshot(snapshot))
	require.Error(t, func() error {
		_, err := issueagent.CandidateSnapshotDigest(snapshot)
		return err
	}())

	snapshot.ChangeSet.Files[0].ContentBase64 = issueagent.EncodeFileContent(nil)
	snapshot.TaskID = "sha256:short"
	require.Error(t, issueagent.ValidateCandidateSnapshot(snapshot))
}
