package reviewagentverify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCommandEntersNetworkNamespaceBeforeSandbox(t *testing.T) {
	t.Parallel()

	executor := &OSExecutor{
		environment:   []string{"PATH=/usr/bin", "HOME=/tmp/home"},
		homeDir:       "/tmp/home",
		tempDir:       "/tmp/check-tmp",
		sandboxBinary: "/usr/bin/bwrap",
		gitBinary:     "/usr/bin/git",
		helperBinary:  "/tmp/wkreviewcheck",
		networkFence:  "/tmp/review-agent-network-fence.sh",
		networkPID:    "/tmp/review-agent-netns.pid",
		runnerTemp:    "/tmp",
	}
	sandbox := &processSandbox{
		workspace:  "/tmp/workspace",
		home:       "/tmp/sandbox-home",
		temp:       "/tmp/sandbox-tmp",
		workingDir: "/tmp/workspace/internal",
	}

	executable, arguments, commandDir, environment := executor.processCommand(
		[]string{"review-agent-check", "go-unit"},
		sandbox.workingDir,
		sandbox,
	)

	require.Equal(t, executor.networkFence, executable)
	require.Equal(
		t,
		[]string{
			"join",
			executor.networkPID,
			executor.sandboxBinary,
		},
		arguments[:3],
	)
	require.Contains(t, arguments, "--ro-bind")
	require.Contains(t, arguments, executor.helperBinary)
	require.Equal(t, string(filepath.Separator), commandDir)
	require.Equal(
		t,
		[]string{"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=/tmp"},
		environment,
	)

	gitCommand := executor.trustedGitCommand("status", "--short")
	require.Equal(t, executor.networkFence, gitCommand.Path)
	require.Equal(
		t,
		[]string{
			executor.networkFence,
			"join",
			executor.networkPID,
			executor.gitBinary,
			"status",
			"--short",
		},
		gitCommand.Args,
	)
	require.Contains(t, gitCommand.Env, "HOME=/nonexistent")
	require.Contains(t, gitCommand.Env, "RUNNER_TEMP=/tmp")
}
