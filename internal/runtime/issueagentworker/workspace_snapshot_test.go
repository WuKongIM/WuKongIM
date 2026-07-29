package issueagentworker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

func TestSnapshotWorkspaceRejectsSymlinkChains(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "target.awk"), []byte("target\n"), 0o644,
	))
	scratch := filepath.Join(workspace, ".issue-agent-tmp")
	require.NoError(t, os.Mkdir(scratch, 0o755))
	require.NoError(t, os.Symlink(
		filepath.Join("..", "target.awk"),
		filepath.Join(scratch, "hop"),
	))
	require.NoError(t, os.Symlink(
		filepath.Join(".issue-agent-tmp", "hop"),
		filepath.Join(workspace, "alias.awk"),
	))

	_, err := snapshotWorkspace(workspace)
	require.EqualError(t, err, "Worker workspace symlink target is unsafe")
}

func TestRelativeWithinRootRejectsPlatformParentSeparators(t *testing.T) {
	t.Parallel()

	require.False(t, relativeWithinRoot(".."))
	require.False(t, relativeWithinRoot("../outside"))
	require.False(t, relativeWithinRoot(`..\outside`))
	require.True(t, relativeWithinRoot("."))
	require.True(t, relativeWithinRoot(filepath.Join("inside", "file")))
}

func TestDeriveChangeSetRejectsSymlinkStructuralChanges(t *testing.T) {
	t.Parallel()

	link := workspaceFile{symlinkTarget: "target.awk"}
	regular := workspaceFile{
		mode: issueagent.FileModeRegular, content: []byte("regular\n"),
	}
	tests := []struct {
		name   string
		before map[string]workspaceFile
		after  map[string]workspaceFile
	}{
		{
			name:   "added",
			before: map[string]workspaceFile{},
			after:  map[string]workspaceFile{"alias.awk": link},
		},
		{
			name:   "deleted",
			before: map[string]workspaceFile{"alias.awk": link},
			after:  map[string]workspaceFile{},
		},
		{
			name:   "symlink replaced by regular file",
			before: map[string]workspaceFile{"alias.awk": link},
			after:  map[string]workspaceFile{"alias.awk": regular},
		},
		{
			name:   "regular file replaced by symlink",
			before: map[string]workspaceFile{"alias.awk": regular},
			after:  map[string]workspaceFile{"alias.awk": link},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := deriveChangeSet(test.before, test.after)
			require.EqualError(t, err, "Worker workspace symlink changed")
		})
	}
}

func TestDeriveChangeSetCapturesChangesThroughUnchangedSymlink(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	target := filepath.Join(workspace, "target.awk")
	require.NoError(t, os.WriteFile(target, []byte("before\n"), 0o644))
	require.NoError(t, os.Symlink(
		"target.awk", filepath.Join(workspace, "alias.awk"),
	))
	before, err := snapshotWorkspace(workspace)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(target, []byte("after\n"), 0o644))
	after, err := snapshotWorkspace(workspace)
	require.NoError(t, err)
	changeSet, err := deriveChangeSet(before, after)
	require.NoError(t, err)
	require.Equal(t, issueagent.ChangeSet{Files: []issueagent.FileChange{{
		Path:          "target.awk",
		Operation:     issueagent.FileOperationUpsert,
		Mode:          issueagent.FileModeRegular,
		ContentBase64: issueagent.EncodeFileContent([]byte("after\n")),
	}}}, changeSet)
}
