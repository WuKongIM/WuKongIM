package reviewagentverify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSExecutorConstructionSanitizesEnvironmentWithoutRunningProcesses(
	t *testing.T,
) {
	t.Parallel()

	home := t.TempDir()
	temporary := t.TempDir()
	executor, err := NewOSExecutor(OSExecutorConfig{
		HomeDir: home,
		Path:    "/trusted/bin:/usr/bin",
		TempDir: temporary,
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"PATH=/trusted/bin:/usr/bin",
		"HOME=" + home,
		"TMPDIR=" + temporary,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"GOWORK=off",
	}, executor.Environment())

	copyOfEnvironment := executor.Environment()
	copyOfEnvironment[0] = "PATH=/attacker-controlled"
	require.Equal(t, "PATH=/trusted/bin:/usr/bin", executor.Environment()[0])

	var unavailable *OSExecutor
	require.Nil(t, unavailable.Environment())
}

func TestOSExecutorConstructionValidatesSandboxIdentityWithoutExecutingIt(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	temporary := filepath.Join(root, "tmp")
	workspace := filepath.Join(root, "workspace")
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.Mkdir(workspace, 0o700))
	require.NoError(t, os.Mkdir(bin, 0o700))
	sandboxBinary := writeExecutable(t, bin, "sandbox")
	helperBinary := writeExecutable(t, bin, "review-agent-check")
	_ = writeExecutable(t, bin, "git")
	networkFence := writeExecutable(t, bin, "network-fence")
	pidFile := filepath.Join(root, "network.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("123\n"), 0o600))

	executor, err := NewOSExecutor(OSExecutorConfig{
		HomeDir:                 home,
		Path:                    bin,
		TempDir:                 temporary,
		WorkspaceRoot:           workspace,
		SandboxBinary:           sandboxBinary,
		HelperBinary:            helperBinary,
		NetworkFenceBinary:      networkFence,
		NetworkNamespacePIDFile: pidFile,
	})
	require.NoError(t, err)
	require.Equal(t, workspace, executor.workspaceRoot)
	require.Equal(t, sandboxBinary, executor.sandboxBinary)
	require.Equal(t, filepath.Join(bin, "git"), executor.gitBinary)
	require.Equal(t, helperBinary, executor.helperBinary)
	require.Equal(t, networkFence, executor.networkFence)
	require.Equal(t, pidFile, executor.networkPID)
	require.Equal(t, root, executor.runnerTemp)
}

func TestOSExecutorConstructionRejectsUnsafeFilesystemBoundaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	regularFile := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(regularFile, []byte("not a directory"), 0o600))
	directory := filepath.Join(root, "directory")
	require.NoError(t, os.Mkdir(directory, 0o700))
	symlink := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(directory, symlink))

	tests := []struct {
		name   string
		config OSExecutorConfig
	}{
		{name: "root home", config: OSExecutorConfig{HomeDir: string(filepath.Separator), Path: "/bin"}},
		{name: "missing home", config: OSExecutorConfig{HomeDir: filepath.Join(root, "missing"), Path: "/bin"}},
		{name: "file home", config: OSExecutorConfig{HomeDir: regularFile, Path: "/bin"}},
		{name: "symlink home", config: OSExecutorConfig{HomeDir: symlink, Path: "/bin"}},
		{name: "empty path", config: OSExecutorConfig{HomeDir: directory}},
		{name: "NUL path", config: OSExecutorConfig{HomeDir: directory, Path: "bad\x00path"}},
		{name: "root temporary directory", config: OSExecutorConfig{HomeDir: directory, Path: "/bin", TempDir: string(filepath.Separator)}},
		{name: "file temporary directory", config: OSExecutorConfig{HomeDir: directory, Path: "/bin", TempDir: regularFile}},
		{name: "symlink temporary directory", config: OSExecutorConfig{HomeDir: directory, Path: "/bin", TempDir: symlink}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewOSExecutor(test.config)
			require.Error(t, err)
		})
	}
}

func TestOSExecutorRejectsIncompleteOrUntrustedSandboxConfiguration(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.Mkdir(workspace, 0o700))
	require.NoError(t, os.Mkdir(bin, 0o700))
	executable := writeExecutable(t, bin, "executable")
	nonExecutable := filepath.Join(bin, "non-executable")
	require.NoError(t, os.WriteFile(nonExecutable, []byte("fixture"), 0o600))
	symlinkExecutable := filepath.Join(bin, "symlink-executable")
	require.NoError(t, os.Symlink(executable, symlinkExecutable))
	_ = writeExecutable(t, bin, "git")

	base := OSExecutorConfig{
		HomeDir:       home,
		Path:          bin,
		WorkspaceRoot: workspace,
		SandboxBinary: executable,
		HelperBinary:  executable,
	}
	tests := []struct {
		name   string
		mutate func(*OSExecutorConfig)
	}{
		{name: "sandbox lacks workspace", mutate: func(config *OSExecutorConfig) { config.WorkspaceRoot = "" }},
		{name: "workspace lacks sandbox", mutate: func(config *OSExecutorConfig) { config.SandboxBinary = "" }},
		{name: "relative sandbox", mutate: func(config *OSExecutorConfig) { config.SandboxBinary = "relative" }},
		{name: "non executable sandbox", mutate: func(config *OSExecutorConfig) { config.SandboxBinary = nonExecutable }},
		{name: "symlink sandbox", mutate: func(config *OSExecutorConfig) { config.SandboxBinary = symlinkExecutable }},
		{name: "root workspace", mutate: func(config *OSExecutorConfig) { config.WorkspaceRoot = string(filepath.Separator) }},
		{name: "file workspace", mutate: func(config *OSExecutorConfig) { config.WorkspaceRoot = nonExecutable }},
		{name: "relative helper", mutate: func(config *OSExecutorConfig) { config.HelperBinary = "relative" }},
		{name: "non executable helper", mutate: func(config *OSExecutorConfig) { config.HelperBinary = nonExecutable }},
		{name: "missing trusted git", mutate: func(config *OSExecutorConfig) { config.Path = filepath.Join(root, "missing-bin") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			test.mutate(&config)
			_, err := NewOSExecutor(config)
			require.Error(t, err)
		})
	}
}

func TestNetworkNamespaceBoundaryRequiresTrustedFilesAndProcessSandbox(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.Mkdir(workspace, 0o700))
	require.NoError(t, os.Mkdir(bin, 0o700))
	executable := writeExecutable(t, bin, "sandbox")
	helper := writeExecutable(t, bin, "helper")
	_ = writeExecutable(t, bin, "git")
	pidFile := filepath.Join(root, "network.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("123\n"), 0o600))

	_, err := NewOSExecutor(OSExecutorConfig{
		HomeDir:                 home,
		Path:                    bin,
		NetworkFenceBinary:      executable,
		NetworkNamespacePIDFile: pidFile,
	})
	require.EqualError(t, err, "named-check network fence requires process sandbox")

	base := OSExecutorConfig{
		HomeDir:                 home,
		Path:                    bin,
		WorkspaceRoot:           workspace,
		SandboxBinary:           executable,
		HelperBinary:            helper,
		NetworkFenceBinary:      executable,
		NetworkNamespacePIDFile: pidFile,
	}
	tests := []struct {
		name   string
		mutate func(*OSExecutorConfig)
	}{
		{name: "relative fence", mutate: func(config *OSExecutorConfig) { config.NetworkFenceBinary = "relative" }},
		{name: "relative PID file", mutate: func(config *OSExecutorConfig) { config.NetworkNamespacePIDFile = "network.pid" }},
		{name: "missing PID file", mutate: func(config *OSExecutorConfig) { config.NetworkNamespacePIDFile = filepath.Join(root, "missing.pid") }},
		{name: "PID directory", mutate: func(config *OSExecutorConfig) { config.NetworkNamespacePIDFile = root }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			test.mutate(&config)
			_, err := NewOSExecutor(config)
			require.Error(t, err)
		})
	}
}

func TestFilesystemBoundaryHelpersRejectEscapesAndSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "one", "two")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	require.NoError(t, secureDirectoryWithin(root, root))
	require.NoError(t, secureDirectoryWithin(root, nested))
	require.Error(t, secureDirectoryWithin(root, filepath.Dir(root)))
	require.Error(t, secureDirectoryWithin(root, filepath.Join(root, "missing")))

	symlink := filepath.Join(root, "linked")
	require.NoError(t, os.Symlink(nested, symlink))
	require.Error(t, secureDirectoryWithin(root, symlink))

	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	executable := writeExecutable(t, bin, "trusted")
	require.Equal(t, executable, mustExecutable(t, executable))
	_, err := validateExecutable(filepath.Join(bin, "missing"), "fixture")
	require.Error(t, err)
	_, err = validateExecutable("relative", "fixture")
	require.Error(t, err)

	trusted, err := executableInPath(
		strings.Join([]string{"relative", bin}, string(os.PathListSeparator)),
		"trusted",
	)
	require.NoError(t, err)
	require.Equal(t, executable, trusted)
	_, err = executableInPath(bin, "missing")
	require.EqualError(t, err, "trusted git executable is unavailable")
}

func TestProcessCommandAndBoundedBufferPreserveFixedBoundaries(t *testing.T) {
	t.Parallel()

	executor := &OSExecutor{environment: []string{"PATH=/trusted", "HOME=/safe"}}
	executable, arguments, directory, environment := executor.processCommand(
		[]string{"go", "test", "./internal/..."},
		"/workspace",
		nil,
	)
	require.Equal(t, "go", executable)
	require.Equal(t, []string{"test", "./internal/..."}, arguments)
	require.Equal(t, "/workspace", directory)
	require.Equal(t, executor.Environment(), environment)

	gitEnvironment := trustedGitEnvironment("/trusted/bin/git")
	require.Equal(t, "PATH=/trusted/bin", gitEnvironment[0])
	require.Contains(t, gitEnvironment, "GIT_CONFIG_GLOBAL=/dev/null")
	command := (&OSExecutor{gitBinary: "/trusted/bin/git"}).trustedGitCommand(
		"status",
		"--short",
	)
	require.Equal(t, "/trusted/bin/git", command.Path)
	require.Equal(t, []string{"/trusted/bin/git", "status", "--short"}, command.Args)
	require.Equal(t, gitEnvironment, command.Env)

	buffer := &boundedBuffer{limit: 5}
	written, err := buffer.Write([]byte("abc"))
	require.NoError(t, err)
	require.Equal(t, 3, written)
	written, err = buffer.Write([]byte("defg"))
	require.NoError(t, err)
	require.Equal(t, 4, written)
	require.Equal(t, []byte("abcde"), buffer.Bytes())
	require.True(t, buffer.Exceeded())
	written, err = buffer.Write([]byte("ignored"))
	require.NoError(t, err)
	require.Equal(t, 7, written)
	require.Equal(t, []byte("abcde"), buffer.Bytes())
}

func writeExecutable(t *testing.T, directory string, name string) string {
	t.Helper()
	pathValue := filepath.Join(directory, name)
	require.NoError(t, os.WriteFile(pathValue, []byte("fixture"), 0o700))
	return pathValue
}

func mustExecutable(t *testing.T, pathValue string) string {
	t.Helper()
	result, err := validateExecutable(pathValue, "fixture")
	require.NoError(t, err)
	return result
}
