package issueagentverify_test

import (
	"os"
	"path/filepath"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentverify"
	"github.com/stretchr/testify/require"
)

func TestCaptureCandidateUsesFilesystemInsteadOfCodexGitState(t *testing.T) {
	t.Parallel()

	baseline := t.TempDir()
	workspace := t.TempDir()
	writeCandidateFile(t, baseline, "internal/example/fix.go", "package example\n")
	writeCandidateFile(t, workspace, "internal/example/fix.go",
		"package example\n\nfunc fixed() bool { return true }\n")
	writeCandidateFile(t, workspace, "internal/example/fix_test.go",
		"package example\n")
	writeCandidateFile(t, workspace, ".git/config",
		"[core]\nworktree = /tmp/forged\n")

	snapshot, err := issueagentverify.CaptureCandidate(
		baseline,
		workspace,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"0123456789abcdef0123456789abcdef01234567",
		issueagentverify.CaptureLimits{
			MaxFiles: 8, MaxFileBytes: 1 << 20,
			MaxTotalBytes: 2 << 20, MaxDeletions: 4,
		},
	)
	require.NoError(t, err)
	require.Len(t, snapshot.ChangeSet.Files, 2)
	require.Equal(t, "internal/example/fix.go", snapshot.ChangeSet.Files[0].Path)
	require.Equal(t, "internal/example/fix_test.go", snapshot.ChangeSet.Files[1].Path)
	require.NotContains(t, snapshot.ChangeSet.Files, contract.FileChange{
		Path: ".git/config",
	})
}

func writeCandidateFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
