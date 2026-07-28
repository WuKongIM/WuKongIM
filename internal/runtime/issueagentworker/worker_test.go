package issueagentworker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

func TestWorkerDerivesChangeSetAndEvidenceInsteadOfTrustingModel(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "pkg", "example"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "pkg", "example", "fix.go"),
		[]byte("package example\n\nconst fixed = false\n"), 0o644,
	))
	task := validWorkerTask()
	prompt := []byte("fixed worker prompt")
	task.PromptDigest = digestForTest(prompt)
	policy := []byte(`{"enabled":true}`)
	task.PolicyDigest = digestForTest(policy)
	task.InstructionDigests = []issueagent.FileDigest{{
		Path:   "pkg/example/fix.go",
		SHA256: digestForTest([]byte("package example\n\nconst fixed = false\n")),
	}}
	runner := &fakeRunner{result: issueagentworker.ExecResult{
		ExitCode: 0, Stdout: []byte("ok"), Duration: time.Second,
	}}
	worker, err := issueagentworker.NewWorker(issueagentworker.WorkerConfig{
		Task: task, Prompt: prompt, Policy: policy, Workspace: workspace,
		Runner: runner,
		Model: func(
			ctx context.Context,
			task issueagent.TaskEnvelope,
			_ []byte,
			broker *issueagentworker.Broker,
		) (issueagentworker.ModelOutput, error) {
			before, err := broker.Read(ctx, "pkg/example/fix.go")
			require.NoError(t, err)
			_, err = broker.Apply(ctx, issueagentworker.ApplyRequest{
				Path: "pkg/example/fix.go", ExpectedSHA256: before.SHA256,
				ContentBase64: issueagent.EncodeFileContent(
					[]byte("package example\n\nconst fixed = true\n"),
				),
			})
			require.NoError(t, err)
			_, err = broker.RunCommand(ctx, issueagentworker.CommandRequest{
				Argv:       []string{"go", "test", "./pkg/example"},
				WorkingDir: ".", Timeout: time.Minute, OutputLimit: 1024,
			})
			require.NoError(t, err)
			return issueagentworker.ModelOutput{
				Result: issueagent.AgentResult{
					SchemaVersion: 1, Repository: task.Repository,
					IssueNumber: task.IssueNumber, Generation: task.Generation,
					Sequence: task.Sequence, OperationID: task.OperationID,
					Phase: task.Phase, Status: issueagent.ResultStatusSuccess,
					RequestedState:  issueagent.StateValidating,
					RequestedAction: issueagent.ActionValidate,
					ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
						Path: "attacker", Operation: issueagent.FileOperationDelete,
					}}},
				},
				Usage: issueagent.ModelUsage{
					Provider: task.Provider, Model: task.Model,
					InputTokens: 10, OutputTokens: 5,
				},
			}, nil
		},
		MaxArtifactBytes: 1 << 20,
	})
	require.NoError(t, err)
	artifact, err := worker.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, artifact.Result.ChangeSet.Files, 1)
	require.Equal(t, "pkg/example/fix.go", artifact.Result.ChangeSet.Files[0].Path)
	require.NotEqual(t, "attacker", artifact.Result.ChangeSet.Files[0].Path)
	require.Len(t, artifact.Result.Evidence.Commands, 1)
	require.NotEmpty(t, artifact.SHA256)
}

func digestForTest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validWorkerTask() issueagent.TaskEnvelope {
	return issueagent.TaskEnvelope{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 5,
		OperationID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:              issueagent.PhaseFix,
		CheckpointDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		AffectedSHA:        "0123456789abcdef0123456789abcdef01234567",
		DiagnosisBaseSHA:   "1234567890abcdef1234567890abcdef12345678",
		FrozenIssue:        "deterministic bug",
		AcceptedCommentIDs: []int64{},
		AllowedPaths:       []string{"pkg/example"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 8,
		}},
		Limits: issueagent.ResourceLimits{
			WallTime: 20 * time.Minute, MaxOutputBytes: 1 << 20,
			MaxFiles: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20,
		},
		Provider: issueagent.ProviderDeepSeek, Model: "policy-model",
	}
}
