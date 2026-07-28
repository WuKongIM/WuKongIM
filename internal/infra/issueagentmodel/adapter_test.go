package issueagentmodel_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func validAdapterTaskAndResult(
	t *testing.T,
) (issueagent.TaskEnvelope, issueagent.AgentResult) {
	t.Helper()
	task := issueagent.TaskEnvelope{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 3,
		OperationID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:              issueagent.PhaseReproduce,
		CheckpointDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PolicyDigest:       "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		PromptDigest:       promptDigest("fixed prompt"),
		AffectedSHA:        "0123456789abcdef0123456789abcdef01234567",
		DiagnosisBaseSHA:   "1234567890abcdef1234567890abcdef12345678",
		FrozenIssue:        "version: v2.0.0\nrepro: deterministic",
		AcceptedCommentIDs: []int64{},
		InstructionDigests: []issueagent.FileDigest{{
			Path:   "AGENTS.md",
			SHA256: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		}},
		AllowedPaths: []string{"test/e2e/example"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 8,
		}},
		Limits: issueagent.ResourceLimits{
			WallTime: 20 * time.Minute, MaxOutputBytes: 1 << 20,
			MaxFiles: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20,
		},
		Provider: issueagent.ProviderDeepSeek, Model: "deepseek-chat",
	}
	result := issueagent.AgentResult{
		SchemaVersion: 1, Repository: task.Repository,
		IssueNumber: task.IssueNumber, Generation: task.Generation,
		Sequence: task.Sequence, OperationID: task.OperationID,
		Phase: task.Phase, Status: issueagent.ResultStatusSuccess,
		RequestedState:  issueagent.StateReproduced,
		RequestedAction: issueagent.ActionOpenDraftPR,
		ChangeSet:       issueagent.ChangeSet{Files: []issueagent.FileChange{}},
		Evidence: issueagent.EvidenceManifest{
			ArtifactSHA256: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			Commands: []issueagent.CommandEvidence{{
				Executable: "go", Arguments: []string{"test", "./test/e2e/example"},
				WorkingDir: ".", ExitCode: 1,
				StdoutSHA256: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				StderrSHA256: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
				DurationMS:   1000,
			}},
		},
		Usage: issueagent.ModelUsage{
			Provider: task.Provider, Model: task.Model,
			InputTokens: 30, OutputTokens: 15,
		},
	}
	require.NoError(t, issueagent.ValidateAgentResult(result, task))
	return task, result
}

func promptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return "sha256:" + hex.EncodeToString(sum[:])
}
