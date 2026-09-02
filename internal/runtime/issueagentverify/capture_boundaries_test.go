package issueagentverify

import (
	"os"
	"path/filepath"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

const (
	testIssueAgentTaskID  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testIssueAgentBaseSHA = "0123456789abcdef0123456789abcdef01234567"
)

func TestCaptureCandidatePreservesFilesystemIdentity(t *testing.T) {
	t.Parallel()

	baseline := t.TempDir()
	workspace := t.TempDir()
	for _, root := range []string{baseline, workspace} {
		writeIssueAgentFile(t, root, "docs/target.txt", []byte("stable\n"), 0o644)
		require.NoError(t, os.Symlink(
			"target.txt",
			filepath.Join(root, "docs", "stable-link"),
		))
	}
	writeIssueAgentFile(t, baseline, "scripts/run.sh", []byte("old\n"), 0o644)
	writeIssueAgentFile(t, workspace, "scripts/run.sh", []byte("new\n"), 0o755)
	writeIssueAgentFile(t, baseline, "obsolete.txt", []byte("remove\n"), 0o644)

	snapshot, err := CaptureCandidate(
		baseline,
		workspace,
		testIssueAgentTaskID,
		testIssueAgentBaseSHA,
		CaptureLimits{
			MaxFiles: 4, MaxFileBytes: 1024,
			MaxTotalBytes: 2048, MaxDeletions: 1,
		},
	)
	require.NoError(t, err)
	require.Equal(t, []contract.FileChange{
		{
			Path:      "obsolete.txt",
			Operation: contract.FileOperationDelete,
		},
		{
			Path:          "scripts/run.sh",
			Operation:     contract.FileOperationUpsert,
			Mode:          contract.FileModeExecutable,
			ContentBase64: contract.EncodeFileContent([]byte("new\n")),
		},
	}, snapshot.ChangeSet.Files)

	digest, err := CandidateSnapshotDigest(snapshot)
	require.NoError(t, err)
	require.Regexp(t, candidateDigestPattern, digest)
	require.NoError(t, ValidateCandidateSnapshot(snapshot))
}

func TestCaptureCandidateRejectsSymlinkTopologyChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		prepareBaseline  func(*testing.T, string)
		prepareWorkspace func(*testing.T, string)
	}{
		{
			name: "new symlink",
			prepareWorkspace: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "target", []byte("data"), 0o644)
				require.NoError(t, os.Symlink("target", filepath.Join(root, "link")))
			},
		},
		{
			name: "removed symlink",
			prepareBaseline: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "target", []byte("data"), 0o644)
				require.NoError(t, os.Symlink("target", filepath.Join(root, "link")))
			},
		},
		{
			name: "retargeted symlink",
			prepareBaseline: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "first", []byte("one"), 0o644)
				writeIssueAgentFile(t, root, "second", []byte("two"), 0o644)
				require.NoError(t, os.Symlink("first", filepath.Join(root, "link")))
			},
			prepareWorkspace: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "first", []byte("one"), 0o644)
				writeIssueAgentFile(t, root, "second", []byte("two"), 0o644)
				require.NoError(t, os.Symlink("second", filepath.Join(root, "link")))
			},
		},
		{
			name: "regular file replaced by symlink",
			prepareBaseline: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "link", []byte("ordinary"), 0o644)
				writeIssueAgentFile(t, root, "target", []byte("data"), 0o644)
			},
			prepareWorkspace: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "target", []byte("data"), 0o644)
				require.NoError(t, os.Symlink("target", filepath.Join(root, "link")))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			baseline := t.TempDir()
			workspace := t.TempDir()
			if test.prepareBaseline != nil {
				test.prepareBaseline(t, baseline)
			}
			if test.prepareWorkspace != nil {
				test.prepareWorkspace(t, workspace)
			}
			_, err := CaptureCandidate(
				baseline, workspace,
				testIssueAgentTaskID, testIssueAgentBaseSHA,
				CaptureLimits{
					MaxFiles: 10, MaxFileBytes: 1024,
					MaxTotalBytes: 4096, MaxDeletions: 10,
				},
			)
			require.ErrorContains(t, err, "changed symlink")
		})
	}
}

func TestCandidateSymlinkValidationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		setup  func(*testing.T, string)
		want   string
	}{
		{
			name: "valid parent relative target", target: "../target.txt",
			setup: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "target.txt", []byte("data"), 0o644)
			},
		},
		{name: "absolute", target: string(filepath.Separator) + "tmp", want: "target is invalid"},
		{name: "not normalized", target: "./target.txt", want: "not normalized"},
		{name: "escape", target: "../../outside", want: "escapes workspace"},
		{name: "dangling", target: "missing", want: "does not target a regular file"},
		{
			name: "directory", target: "target-dir", want: "does not target a regular file",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.Mkdir(filepath.Join(root, "target-dir"), 0o755))
			},
		},
		{
			name: "symlink chain", target: "first-link", want: "does not target a regular file",
			setup: func(t *testing.T, root string) {
				writeIssueAgentFile(t, root, "target.txt", []byte("data"), 0o644)
				require.NoError(t, os.Symlink("target.txt", filepath.Join(root, "first-link")))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if test.setup != nil {
				test.setup(t, root)
			}
			linkDir := filepath.Join(root, "nested")
			require.NoError(t, os.Mkdir(linkDir, 0o755))
			link := filepath.Join(linkDir, "link")
			require.NoError(t, os.Symlink(test.target, link))
			got, err := validateCandidateSymlink(root, link)
			if test.want == "" {
				require.NoError(t, err)
				require.Equal(t, test.target, got)
				return
			}
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestCaptureCandidateEnforcesTrustedBounds(t *testing.T) {
	t.Parallel()

	baseline := t.TempDir()
	workspace := t.TempDir()
	writeIssueAgentFile(t, workspace, "large.txt", []byte("12345"), 0o644)
	validLimits := CaptureLimits{
		MaxFiles: 1, MaxFileBytes: 4,
		MaxTotalBytes: 4, MaxDeletions: 0,
	}
	_, err := CaptureCandidate(
		baseline, workspace,
		testIssueAgentTaskID, testIssueAgentBaseSHA,
		validLimits,
	)
	require.ErrorContains(t, err, "exceeds byte limit")

	invalidInputs := []struct {
		name    string
		taskID  string
		baseSHA string
		limits  CaptureLimits
	}{
		{name: "task id", taskID: "bad", baseSHA: testIssueAgentBaseSHA, limits: validLimits},
		{name: "base sha", taskID: testIssueAgentTaskID, baseSHA: "bad", limits: validLimits},
		{name: "file count", taskID: testIssueAgentTaskID, baseSHA: testIssueAgentBaseSHA, limits: CaptureLimits{MaxFileBytes: 1, MaxTotalBytes: 1}},
		{name: "file bytes", taskID: testIssueAgentTaskID, baseSHA: testIssueAgentBaseSHA, limits: CaptureLimits{MaxFiles: 1, MaxTotalBytes: 1}},
		{name: "total bytes", taskID: testIssueAgentTaskID, baseSHA: testIssueAgentBaseSHA, limits: CaptureLimits{MaxFiles: 1, MaxFileBytes: 1}},
		{name: "deletions", taskID: testIssueAgentTaskID, baseSHA: testIssueAgentBaseSHA, limits: CaptureLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1, MaxDeletions: -1}},
	}
	for _, test := range invalidInputs {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := CaptureCandidate(
				baseline, workspace, test.taskID, test.baseSHA, test.limits,
			)
			require.EqualError(t, err, "candidate capture input is invalid")
		})
	}
}

func TestCaptureCandidateReportsTreeAndAggregateLimitFailures(t *testing.T) {
	t.Parallel()

	validLimits := CaptureLimits{
		MaxFiles: 4, MaxFileBytes: 16,
		MaxTotalBytes: 16, MaxDeletions: 4,
	}
	_, err := CaptureCandidate(
		"relative", t.TempDir(),
		testIssueAgentTaskID, testIssueAgentBaseSHA, validLimits,
	)
	require.ErrorContains(t, err, "scan candidate baseline")
	_, err = CaptureCandidate(
		t.TempDir(), "relative",
		testIssueAgentTaskID, testIssueAgentBaseSHA, validLimits,
	)
	require.ErrorContains(t, err, "scan candidate workspace")

	baseline := t.TempDir()
	workspace := t.TempDir()
	writeIssueAgentFile(t, workspace, "one", []byte("123"), 0o644)
	writeIssueAgentFile(t, workspace, "two", []byte("456"), 0o644)
	_, err = CaptureCandidate(
		baseline, workspace,
		testIssueAgentTaskID, testIssueAgentBaseSHA,
		CaptureLimits{
			MaxFiles: 1, MaxFileBytes: 3,
			MaxTotalBytes: 6, MaxDeletions: 0,
		},
	)
	require.ErrorContains(t, err, "limit")
	_, err = CaptureCandidate(
		baseline, workspace,
		testIssueAgentTaskID, testIssueAgentBaseSHA,
		CaptureLimits{
			MaxFiles: 2, MaxFileBytes: 3,
			MaxTotalBytes: 5, MaxDeletions: 0,
		},
	)
	require.ErrorContains(t, err, "total byte limit")

	writeIssueAgentFile(t, baseline, "removed", []byte("old"), 0o644)
	_, err = CaptureCandidate(
		baseline, t.TempDir(),
		testIssueAgentTaskID, testIssueAgentBaseSHA,
		CaptureLimits{
			MaxFiles: 2, MaxFileBytes: 3,
			MaxTotalBytes: 3, MaxDeletions: 0,
		},
	)
	require.ErrorContains(t, err, "deletions")
}

func TestCandidateTreeRejectsUnsafeRootsAndPermissions(t *testing.T) {
	t.Parallel()

	_, err := scanCandidateTree("relative")
	require.EqualError(t, err, "candidate tree root is invalid")
	missing := filepath.Join(t.TempDir(), "missing")
	_, err = scanCandidateTree(missing)
	require.EqualError(t, err, "candidate tree root is unsafe")
	regular := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(regular, []byte("data"), 0o644))
	_, err = scanCandidateTree(regular)
	require.EqualError(t, err, "candidate tree root is unsafe")
	root := t.TempDir()
	rootLink := filepath.Join(t.TempDir(), "root-link")
	require.NoError(t, os.Symlink(root, rootLink))
	_, err = scanCandidateTree(rootLink)
	require.EqualError(t, err, "candidate tree root is unsafe")

	writeIssueAgentFile(t, root, "private.txt", []byte("secret"), 0o600)
	_, err = scanCandidateTree(root)
	require.ErrorContains(t, err, "unsupported permissions")
	require.Equal(t, contract.FileModeRegular, mustCandidateMode(t, 0o644))
	require.Equal(t, contract.FileModeExecutable, mustCandidateMode(t, 0o755))
}

func mustCandidateMode(t *testing.T, mode os.FileMode) contract.FileMode {
	t.Helper()
	result, err := candidateFileMode(mode)
	require.NoError(t, err)
	return result
}

func writeIssueAgentFile(
	t *testing.T,
	root string,
	repositoryPath string,
	content []byte,
	mode os.FileMode,
) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(repositoryPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, content, mode))
	require.NoError(t, os.Chmod(target, mode))
}
