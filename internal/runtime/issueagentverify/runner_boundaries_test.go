package issueagentverify

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessRunnerConstructionKeepsFilesystemBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	temporaryRoot := t.TempDir()
	runner, err := NewProcessRunner(root, temporaryRoot, 4096)
	require.NoError(t, err)
	require.Equal(t, root, runner.root)
	require.Equal(t, temporaryRoot, runner.temporaryRoot)
	require.Equal(t, 4096, runner.maxOutputBytes)

	regularFile := filepath.Join(t.TempDir(), "not-directory")
	require.NoError(t, os.WriteFile(regularFile, []byte("data"), 0o644))
	symlink := filepath.Join(t.TempDir(), "root-link")
	require.NoError(t, os.Symlink(root, symlink))

	for _, test := range []struct {
		name      string
		root      string
		temporary string
		limit     int
	}{
		{name: "relative checkout", root: ".", temporary: temporaryRoot, limit: 1},
		{name: "checkout file", root: regularFile, temporary: temporaryRoot, limit: 1},
		{name: "checkout symlink", root: symlink, temporary: temporaryRoot, limit: 1},
		{name: "relative temporary root", root: root, temporary: ".", limit: 1},
		{name: "zero output limit", root: root, temporary: temporaryRoot, limit: 0},
		{name: "oversized output limit", root: root, temporary: temporaryRoot, limit: (16 << 20) + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewProcessRunner(test.root, test.temporary, test.limit)
			require.EqualError(t, err, "Verifier process runner configuration is invalid")
		})
	}
}

func TestProcessRunnerResolvesOnlyCheckoutDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	temporaryRoot := t.TempDir()
	runner, err := NewProcessRunner(root, temporaryRoot, 1024)
	require.NoError(t, err)

	nested := filepath.Join(root, "internal", "example")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	resolved, err := runner.workingDirectory(".")
	require.NoError(t, err)
	require.Equal(t, root, resolved)
	resolved, err = runner.workingDirectory("internal/example")
	require.NoError(t, err)
	canonicalNested, err := filepath.EvalSymlinks(nested)
	require.NoError(t, err)
	require.Equal(t, canonicalNested, resolved)

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "outside-link")))
	_, err = runner.workingDirectory("outside-link")
	require.EqualError(t, err, "Verifier working directory escapes checkout")

	writeIssueAgentFile(t, root, "ordinary.txt", []byte("data"), 0o644)
	_, err = runner.workingDirectory("ordinary.txt")
	require.EqualError(t, err, "Verifier working directory is invalid")
	_, err = runner.workingDirectory("missing")
	require.EqualError(t, err, "resolve Verifier working directory")
}

func TestProcessRunnerEarlyValidationDoesNotExecute(t *testing.T) {
	t.Parallel()

	_, err := (*ProcessRunner)(nil).Run(context.Background(), VerificationCommandPlan{})
	require.EqualError(t, err, "Verifier process request is invalid")

	runner, err := NewProcessRunner(t.TempDir(), t.TempDir(), 1024)
	require.NoError(t, err)
	_, err = runner.Run(context.Background(), VerificationCommandPlan{})
	require.EqualError(t, err, "Verifier command plan is invalid")
	_, err = runner.Run(nil, VerificationCommandPlan{
		Arguments:  []string{"unused"},
		WorkingDir: ".",
	})
	require.EqualError(t, err, "Verifier process request is invalid")
}

func TestProcessRunnerFailsClosedIfDirectoriesChangeAfterConstruction(t *testing.T) {
	t.Parallel()

	t.Run("working directory disappears", func(t *testing.T) {
		root := t.TempDir()
		runner, err := NewProcessRunner(root, t.TempDir(), 1024)
		require.NoError(t, err)
		_, err = runner.Run(context.Background(), VerificationCommandPlan{
			Arguments: []string{"never-executed"}, WorkingDir: "missing",
		})
		require.EqualError(t, err, "resolve Verifier working directory")
	})

	t.Run("temporary root becomes a file", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		runner, err := NewProcessRunner(t.TempDir(), temporaryRoot, 1024)
		require.NoError(t, err)
		require.NoError(t, os.Remove(temporaryRoot))
		require.NoError(t, os.WriteFile(temporaryRoot, []byte("replaced"), 0o600))
		_, err = runner.Run(context.Background(), VerificationCommandPlan{
			Arguments: []string{"never-executed"}, WorkingDir: ".",
		})
		require.EqualError(t, err, "prepare Verifier process directory")
	})
}

func TestDisableGoTelemetryCreatesPrivateOffMode(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configHome := t.TempDir()
	require.NoError(t, disableGoTelemetry(home, configHome))
	telemetryHome := configHome
	if runtime.GOOS == "darwin" {
		telemetryHome = filepath.Join(home, "Library", "Application Support")
	}
	modeFile := filepath.Join(telemetryHome, "go", "telemetry", "mode")
	content, err := os.ReadFile(modeFile)
	require.NoError(t, err)
	require.Equal(t, "off\n", string(content))
	info, err := os.Stat(modeFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestBoundedProcessBufferReportsFullWritesAndTruncatesEvidence(t *testing.T) {
	t.Parallel()

	buffer := &boundedProcessBuffer{limit: 5}
	written, err := buffer.Write([]byte("abc"))
	require.NoError(t, err)
	require.Equal(t, 3, written)
	require.False(t, buffer.overflow)

	written, err = buffer.Write([]byte("defg"))
	require.NoError(t, err)
	require.Equal(t, 4, written)
	require.True(t, buffer.overflow)
	require.Equal(t, "abcde", buffer.buffer.String())

	written, err = buffer.Write([]byte("later"))
	require.NoError(t, err)
	require.Equal(t, 5, written)
	require.Equal(t, "abcde", buffer.buffer.String())

	empty := &boundedProcessBuffer{}
	written, err = empty.Write(nil)
	require.NoError(t, err)
	require.Zero(t, written)
	require.False(t, empty.overflow)
	written, err = empty.Write([]byte("x"))
	require.NoError(t, err)
	require.Equal(t, 1, written)
	require.True(t, empty.overflow)
}
