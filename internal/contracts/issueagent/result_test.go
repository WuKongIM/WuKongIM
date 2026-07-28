package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestAgentResultBindsTaskAndCarriesOnlyTypedChanges(t *testing.T) {
	t.Parallel()

	task := resultTestTask()
	result := issueagent.AgentResult{
		SchemaVersion:   1,
		Repository:      task.Repository,
		IssueNumber:     task.IssueNumber,
		Generation:      task.Generation,
		Sequence:        task.Sequence,
		OperationID:     task.OperationID,
		Phase:           task.Phase,
		Status:          issueagent.ResultStatusSuccess,
		RequestedState:  issueagent.StateValidating,
		RequestedAction: issueagent.ActionValidate,
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:      "internal/usecase/example/app.go",
			Operation: issueagent.FileOperationUpsert,
			Mode:      issueagent.FileModeRegular,
			Content:   []byte("package example\n"),
		}}},
		Evidence: issueagent.EvidenceManifest{
			ArtifactSHA256: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			Commands: []issueagent.CommandEvidence{{
				Executable:   "go",
				Arguments:    []string{"test", "./internal/usecase/example"},
				WorkingDir:   ".",
				ExitCode:     0,
				StdoutSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				StderrSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				DurationMS:   250,
			}},
		},
		Usage: issueagent.ModelUsage{
			Provider:     task.Provider,
			Model:        task.Model,
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	require.NoError(t, issueagent.ValidateAgentResult(result, task))
}

func TestAgentResultRejectsIdentityAndCommandSmuggling(t *testing.T) {
	t.Parallel()

	task := resultTestTask()
	result := issueagent.AgentResult{
		SchemaVersion:   1,
		Repository:      task.Repository,
		IssueNumber:     task.IssueNumber,
		Generation:      task.Generation + 1,
		Sequence:        task.Sequence,
		OperationID:     task.OperationID,
		Phase:           task.Phase,
		Status:          issueagent.ResultStatusSuccess,
		RequestedState:  issueagent.StateValidating,
		RequestedAction: issueagent.ActionValidate,
		Evidence: issueagent.EvidenceManifest{
			ArtifactSHA256: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			Commands: []issueagent.CommandEvidence{{
				Executable:   "sh",
				Arguments:    []string{"-c", "git push origin main"},
				WorkingDir:   ".",
				StdoutSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				StderrSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				DurationMS:   1,
			}},
		},
		Usage: issueagent.ModelUsage{
			Provider: task.Provider,
			Model:    task.Model,
		},
	}

	require.Error(t, issueagent.ValidateAgentResult(result, task))
}

func resultTestTask() issueagent.TaskEnvelope {
	return issueagent.TaskEnvelope{
		SchemaVersion:    1,
		Repository:       "WuKongIM/WuKongIM",
		IssueNumber:      42,
		Generation:       1,
		Sequence:         3,
		OperationID:      "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Phase:            issueagent.PhaseFix,
		CheckpointDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		PolicyDigest:     "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		PromptDigest:     "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		AffectedSHA:      "0123456789abcdef0123456789abcdef01234567",
		DiagnosisBaseSHA: "89abcdef0123456789abcdef0123456789abcdef",
		FrozenIssue:      "Expected result differs from actual result.",
		InstructionDigests: []issueagent.FileDigest{{
			Path:   "AGENTS.md",
			SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		AllowedPaths: []string{"internal/usecase/example"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go",
			ArgvPrefix: []string{"test"},
			MaxArgs:    8,
		}},
		Limits: issueagent.ResourceLimits{
			WallTime:       20 * time.Minute,
			MaxOutputBytes: 1 << 20,
			MaxFiles:       8,
			MaxTotalBytes:  1 << 20,
		},
		Provider: issueagent.ProviderCodex,
		Model:    "policy-selected",
	}
}
