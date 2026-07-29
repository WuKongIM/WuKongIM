package issueagentworker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

type functionRunner func(
	context.Context,
	issueagentworker.ExecRequest,
) (issueagentworker.ExecResult, error)

func (runner functionRunner) Run(
	ctx context.Context,
	request issueagentworker.ExecRequest,
) (issueagentworker.ExecResult, error) {
	return runner(ctx, request)
}

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
		Binaries: issueagentworker.BinaryEvidence{
			AffectedSHA256:      digestForTest([]byte("affected")),
			DiagnosisBaseSHA256: digestForTest([]byte("diagnosis-base")),
		},
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
				Argv: append(
					[]string{task.AllowedCommands[0].Executable},
					task.AllowedCommands[0].ArgvPrefix...,
				),
				WorkingDir: ".", Timeout: time.Minute, OutputLimit: 1024,
			})
			require.NoError(t, err)
			_, err = broker.RunCommand(ctx, issueagentworker.CommandRequest{
				Argv: append(
					[]string{task.AllowedCommands[1].Executable},
					task.AllowedCommands[1].ArgvPrefix...,
				),
				WorkingDir: ".", Timeout: time.Minute, OutputLimit: 1024,
			})
			require.NoError(t, err)
			for run := 0; run < task.RequiredRuns; run++ {
				_, err = broker.RunCommand(ctx, issueagentworker.CommandRequest{
					Argv: append(
						[]string{task.AllowedCommands[2].Executable},
						task.AllowedCommands[2].ArgvPrefix...,
					),
					WorkingDir: ".", Timeout: time.Minute, OutputLimit: 1024,
				})
				require.NoError(t, err)
			}
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
	require.NoError(t, issueagentworker.ValidateArtifact(artifact))
	require.Len(t, artifact.Result.ChangeSet.Files, 1)
	require.Equal(t, "pkg/example/fix.go", artifact.Result.ChangeSet.Files[0].Path)
	require.NotEqual(t, "attacker", artifact.Result.ChangeSet.Files[0].Path)
	require.Len(t, artifact.Result.Evidence.Commands, 5)
	require.NotEmpty(t, artifact.SHA256)
}

func TestWorkerRequiresThreeSameAssertionFailuresForBothBaselines(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	scenario := filepath.Join(workspace, "test", "e2e", "issue_agent", "issue_42")
	require.NoError(t, os.MkdirAll(scenario, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "AGENTS.md"), []byte("trusted\n"), 0o644,
	))
	prompt := []byte("write one focused E2E and run both baselines")
	policy := []byte(`{"enabled":true}`)
	task := issueagent.TaskEnvelope{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 4,
		OperationID:      digestForTest([]byte("operation")),
		Phase:            issueagent.PhaseReproduce,
		CheckpointDigest: digestForTest([]byte("checkpoint")),
		PolicyDigest:     digestForTest(policy),
		PromptDigest:     digestForTest(prompt),
		AffectedSHA:      "0123456789abcdef0123456789abcdef01234567",
		DiagnosisBaseSHA: "1234567890abcdef1234567890abcdef12345678",
		FrozenIssue:      "deterministic delivery bug",
		InstructionDigests: []issueagent.FileDigest{{
			Path: "AGENTS.md", SHA256: digestForTest([]byte("trusted\n")),
		}},
		AllowedPaths: []string{"test/e2e/issue_agent/issue_42"},
		AllowedCommands: []issueagent.CommandRule{
			{
				Executable: "env",
				ArgvPrefix: []string{
					"WK_E2E_BINARY=/issue-agent/bin/affected", "go", "test",
					"-tags=e2e", "./test/e2e/issue_agent/issue_42", "-count=1",
				},
				MaxArgs: 6,
			},
			{
				Executable: "env",
				ArgvPrefix: []string{
					"WK_E2E_BINARY=/issue-agent/bin/diagnosis-base", "go", "test",
					"-tags=e2e", "./test/e2e/issue_agent/issue_42", "-count=1",
				},
				MaxArgs: 6,
			},
		},
		Limits: issueagent.ResourceLimits{
			WallTime: 20 * time.Minute, MaxOutputBytes: 1 << 20,
			MaxFiles: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20,
		},
		RequiredTopology: "three-node-cluster", RequiredRuns: 3,
		Provider: issueagent.ProviderDeepSeek, Model: "deepseek-chat",
	}
	assertion := digestForTest([]byte("delivered exactly once"))
	runner := functionRunner(func(
		_ context.Context,
		_ issueagentworker.ExecRequest,
	) (issueagentworker.ExecResult, error) {
		return issueagentworker.ExecResult{
			ExitCode: 1,
			Stdout:   []byte("WK_ISSUE_AGENT_ASSERTION_FAILED " + assertion + "\n"),
			Duration: time.Second,
		}, nil
	})
	worker, err := issueagentworker.NewWorker(issueagentworker.WorkerConfig{
		Task: task, Prompt: prompt, Policy: policy, Workspace: workspace,
		Runner: runner,
		Binaries: issueagentworker.BinaryEvidence{
			AffectedSHA256:      digestForTest([]byte("affected")),
			DiagnosisBaseSHA256: digestForTest([]byte("diagnosis-base")),
		},
		Model: func(
			ctx context.Context,
			task issueagent.TaskEnvelope,
			_ []byte,
			broker *issueagentworker.Broker,
		) (issueagentworker.ModelOutput, error) {
			_, err := broker.Apply(ctx, issueagentworker.ApplyRequest{
				Path: "test/e2e/issue_agent/issue_42/reproduction_test.go",
				ContentBase64: issueagent.EncodeFileContent(
					[]byte("package issue42\n"),
				),
			})
			require.NoError(t, err)
			for _, rule := range task.AllowedCommands {
				for run := 0; run < task.RequiredRuns; run++ {
					argv := append([]string{rule.Executable}, rule.ArgvPrefix...)
					_, err := broker.RunCommand(ctx, issueagentworker.CommandRequest{
						Argv: argv, WorkingDir: ".", Timeout: time.Minute,
						OutputLimit: 1024,
					})
					require.NoError(t, err)
				}
			}
			return issueagentworker.ModelOutput{
				Result: issueagent.AgentResult{
					SchemaVersion: 1, Repository: task.Repository,
					IssueNumber: task.IssueNumber, Generation: task.Generation,
					Sequence: task.Sequence, OperationID: task.OperationID,
					Phase: task.Phase, Status: issueagent.ResultStatusSuccess,
					RequestedState:  issueagent.StateReproduced,
					RequestedAction: issueagent.ActionOpenDraftPR,
					Reproduction: &issueagent.ReproductionClaim{
						Assertion:       "delivered exactly once",
						AssertionSHA256: assertion,
						Topology:        "three-node-cluster",
					},
				},
				Usage: issueagent.ModelUsage{
					Provider: task.Provider, Model: task.Model,
				},
			}, nil
		},
		MaxArtifactBytes: 1 << 20,
	})
	require.NoError(t, err)
	artifact, err := worker.Run(context.Background())
	require.NoError(t, err)
	require.NoError(t, issueagentworker.ValidateArtifact(artifact))
	require.Len(t, artifact.Result.Evidence.Commands, 6)
	for _, command := range artifact.Result.Evidence.Commands {
		require.Equal(t, assertion, command.AssertionSHA256)
	}
}

func TestWorkerBindsDiagnosisToDerivedEvidence(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "internal", "example"), 0o755))
	source := []byte("package example\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "internal", "example", "example.go"),
		source, 0o644,
	))
	prompt := []byte("diagnose from bounded evidence")
	policy := []byte(`{"enabled":true}`)
	task := validWorkerTask()
	task.Phase = issueagent.PhaseDiagnose
	task.RequiredTopology = ""
	task.RequiredRuns = 0
	task.ProductionChangesAllowed = false
	task.AllowedPaths = []string{"internal"}
	task.AllowedCommands = []issueagent.CommandRule{{
		Executable: "go",
		ArgvPrefix: []string{"test", "./internal/example"},
		MaxArgs:    2,
	}}
	task.PromptDigest = digestForTest(prompt)
	task.PolicyDigest = digestForTest(policy)
	task.InstructionDigests = []issueagent.FileDigest{{
		Path: "internal/example/example.go", SHA256: digestForTest(source),
	}}
	worker, err := issueagentworker.NewWorker(issueagentworker.WorkerConfig{
		Task: task, Prompt: prompt, Policy: policy, Workspace: workspace,
		Runner: functionRunner(func(
			_ context.Context,
			_ issueagentworker.ExecRequest,
		) (issueagentworker.ExecResult, error) {
			return issueagentworker.ExecResult{
				ExitCode: 0, Stdout: []byte("observed causal path"),
				Duration: time.Second,
			}, nil
		}),
		Model: func(
			ctx context.Context,
			task issueagent.TaskEnvelope,
			_ []byte,
			broker *issueagentworker.Broker,
		) (issueagentworker.ModelOutput, error) {
			_, commandErr := broker.RunCommand(ctx, issueagentworker.CommandRequest{
				Argv:       []string{"go", "test", "./internal/example"},
				WorkingDir: ".", Timeout: time.Minute, OutputLimit: 1024,
			})
			require.NoError(t, commandErr)
			return issueagentworker.ModelOutput{
				Result: issueagent.AgentResult{
					SchemaVersion: 1, Repository: task.Repository,
					IssueNumber: task.IssueNumber, Generation: task.Generation,
					Sequence: task.Sequence, OperationID: task.OperationID,
					Phase: task.Phase, Status: issueagent.ResultStatusSuccess,
					RequestedState:  issueagent.StateDiagnosed,
					RequestedAction: issueagent.ActionImplementFix,
					ChangeSet:       issueagent.ChangeSet{Files: []issueagent.FileChange{}},
					Evidence: issueagent.EvidenceManifest{
						Commands: []issueagent.CommandEvidence{},
					},
					Usage: issueagent.ModelUsage{
						Provider: task.Provider, Model: task.Model,
					},
					Diagnosis: &issueagent.Diagnosis{
						Summary:           "the example path violates its invariant",
						ExternalSymptom:   "the black-box assertion fails",
						CausalPath:        "entry to example state transition",
						ViolatedInvariant: "the transition must preserve delivery",
						EvidenceReferences: []string{
							"go test ./internal/example",
						},
						EvidenceSHA256:   "",
						IntendedPaths:    []string{"internal/example"},
						ClusterSemantics: "the invariant applies to every cluster size",
						ValidationSuites: []string{"go-e2e", "go-fast"},
						RiskClasses:      []string{},
					},
				},
				Usage: issueagent.ModelUsage{
					Provider: task.Provider, Model: task.Model,
					InputTokens: 20, OutputTokens: 10,
				},
			}, nil
		},
		MaxArtifactBytes: 1 << 20,
	})
	require.NoError(t, err)

	artifact, err := worker.Run(context.Background())
	require.NoError(t, err)
	require.NoError(t, issueagentworker.ValidateArtifact(artifact))
	require.NotNil(t, artifact.Result.Diagnosis)
	require.Equal(
		t, artifact.Result.Evidence.ArtifactSHA256,
		artifact.Result.Diagnosis.EvidenceSHA256,
	)
}

func TestWorkerTurnsAdapterFailureIntoSanitizedPublishableArtifact(t *testing.T) {
	t.Parallel()

	task := validWorkerTask()
	prompt := []byte("fixed worker prompt")
	policy := []byte(`{"enabled":true}`)
	task.PromptDigest = digestForTest(prompt)
	task.PolicyDigest = digestForTest(policy)
	workspace := t.TempDir()
	instruction := []byte("# Worker instructions\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "AGENTS.md"), instruction, 0o644,
	))
	task.InstructionDigests = []issueagent.FileDigest{{
		Path: "AGENTS.md", SHA256: digestForTest(instruction),
	}}
	worker, err := issueagentworker.NewWorker(issueagentworker.WorkerConfig{
		Task: task, Prompt: prompt, Policy: policy, Workspace: workspace,
		Runner: &fakeRunner{},
		Model: func(
			context.Context,
			issueagent.TaskEnvelope,
			[]byte,
			*issueagentworker.Broker,
		) (issueagentworker.ModelOutput, error) {
			return issueagentworker.ModelOutput{}, errors.New("secret provider detail")
		},
		MaxArtifactBytes: 1 << 20,
	})
	require.NoError(t, err)

	artifact, err := worker.Run(context.Background())
	require.NoError(t, err)
	require.NoError(t, issueagentworker.ValidateArtifact(artifact))
	require.Equal(t, issueagent.ResultStatusFailed, artifact.Result.Status)
	require.Equal(t, issueagent.FailureProvider, artifact.Result.Failure.Class)
	require.Empty(t, artifact.Result.Evidence.Commands)
	require.Empty(t, artifact.Result.ChangeSet.Files)
	require.NotContains(t, artifact.Result.Failure.Summary, "secret")
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
		CandidateSHA:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FrozenIssue:        "deterministic bug",
		AcceptedCommentIDs: []int64{},
		AllowedPaths:       []string{"pkg/example"},
		AllowedCommands: []issueagent.CommandRule{
			{
				Executable: "go",
				ArgvPrefix: []string{
					"build", "-trimpath", "-o", ".issue-agent-tmp/wukongim",
					"./cmd/wukongim",
				},
				MaxArgs: 5,
			},
			{
				Executable: "go", ArgvPrefix: []string{"test", "./pkg/example"},
				MaxArgs: 2,
			},
			{
				Executable: "env",
				ArgvPrefix: []string{
					"WK_E2E_BINARY=.issue-agent-tmp/wukongim", "go", "test",
					"-tags=e2e", "./test/e2e/issue_agent/issue_42", "-count=1",
				},
				MaxArgs: 6,
			},
		},
		Limits: issueagent.ResourceLimits{
			WallTime: 20 * time.Minute, MaxOutputBytes: 1 << 20,
			MaxFiles: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20,
		},
		RequiredTopology: "single-node-cluster", RequiredRuns: 3,
		ProductionChangesAllowed: true,
		Provider:                 issueagent.ProviderDeepSeek, Model: "policy-model",
	}
}
