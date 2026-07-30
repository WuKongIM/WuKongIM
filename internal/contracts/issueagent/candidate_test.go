package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCandidateSnapshotDigestsBindIdentityAndContent(t *testing.T) {
	t.Parallel()

	snapshot := issueagent.CandidateSnapshot{
		SchemaVersion: 2,
		TaskID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BaseSHA:       "0123456789abcdef0123456789abcdef01234567",
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:          "internal/example/fix.go",
			Operation:     issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: issueagent.EncodeFileContent([]byte("package example\n")),
		}}},
	}
	candidateDigest, err := issueagent.CandidateSnapshotDigest(snapshot)
	require.NoError(t, err)
	changeDigest, err := issueagent.ChangeSetDigest(snapshot.ChangeSet)
	require.NoError(t, err)
	require.NotEqual(t, candidateDigest, changeDigest)

	snapshot.BaseSHA = "1123456789abcdef0123456789abcdef01234567"
	changed, err := issueagent.CandidateSnapshotDigest(snapshot)
	require.NoError(t, err)
	require.NotEqual(t, candidateDigest, changed)
}
