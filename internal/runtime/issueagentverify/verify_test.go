package issueagentverify_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentverify"
	"github.com/stretchr/testify/require"
)

func TestVerifyCandidateRejectsProtectedPathBeforeExecutingCode(t *testing.T) {
	t.Parallel()

	runner := &verificationRunnerStub{}
	evidence, err := issueagentverify.VerifyCandidate(
		context.Background(),
		t.TempDir(),
		issueagentverify.CandidateSnapshot{
			SchemaVersion: 2,
			TaskID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BaseSHA:       "0123456789abcdef0123456789abcdef01234567",
			ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
				Path:          ".github/workflows/issue-agent.yml",
				Operation:     contract.FileOperationUpsert,
				Mode:          contract.FileModeRegular,
				ContentBase64: contract.EncodeFileContent([]byte("name: injected\n")),
			}}},
		},
		issueagentverify.VerificationPolicy{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			ProtectedPaths: []string{".github/workflows"},
			RequiredSuites: []string{"focused"},
			Commands: []issueagentverify.VerificationCommandPlan{{
				Arguments:  []string{"go", "test", "./internal/...", "-count=1"},
				WorkingDir: ".",
			}},
		},
		runner,
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	)
	require.NoError(t, err)
	require.False(t, evidence.PublicationEligible)
	require.Equal(t, contract.CandidateRiskHigh, evidence.Risk)
	require.Contains(t, evidence.FailureReason, "protected")
	require.Zero(t, runner.calls)
}

func TestVerifyCandidateRejectsNestedInstructionChange(t *testing.T) {
	t.Parallel()

	runner := &verificationRunnerStub{}
	evidence, err := issueagentverify.VerifyCandidate(
		context.Background(),
		t.TempDir(),
		issueagentverify.CandidateSnapshot{
			SchemaVersion: 2,
			TaskID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BaseSHA:       "0123456789abcdef0123456789abcdef01234567",
			ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
				Path:          "internal/example/FLOW.md",
				Operation:     contract.FileOperationUpsert,
				Mode:          contract.FileModeRegular,
				ContentBase64: contract.EncodeFileContent([]byte("changed\n")),
			}}},
		},
		issueagentverify.VerificationPolicy{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			RequiredSuites: []string{"focused"},
			Commands: []issueagentverify.VerificationCommandPlan{{
				Arguments:  []string{"go", "test", "./internal/example"},
				WorkingDir: ".",
			}},
		},
		runner,
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	)
	require.NoError(t, err)
	require.False(t, evidence.PublicationEligible)
	require.Contains(t, evidence.FailureReason, "protected")
	require.Zero(t, runner.calls)
}

func TestVerifyCandidateAppliesAndTestsLowRiskChange(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	writeCandidateFile(t, checkout, "internal/example/fix.go", "package example\n")
	runner := &verificationRunnerStub{}
	evidence, err := issueagentverify.VerifyCandidate(
		context.Background(),
		checkout,
		issueagentverify.CandidateSnapshot{
			SchemaVersion: 2,
			TaskID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BaseSHA:       "0123456789abcdef0123456789abcdef01234567",
			ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
				Path:      "internal/example/fix.go",
				Operation: contract.FileOperationUpsert,
				Mode:      contract.FileModeRegular,
				ContentBase64: contract.EncodeFileContent(
					[]byte("package example\n\nfunc fixed() bool { return true }\n"),
				),
			}}},
		},
		issueagentverify.VerificationPolicy{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			ProtectedPaths: []string{".github/issue-agent"},
			HighRiskPaths:  []string{"internal/runtime/cluster"},
			RequiredSuites: []string{"focused"},
			Commands: []issueagentverify.VerificationCommandPlan{{
				Arguments:  []string{"go", "test", "./internal/example", "-count=1"},
				WorkingDir: ".",
			}},
			MaxChangedFiles: 8,
		},
		runner,
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	)
	require.NoError(t, err)
	require.True(t, evidence.PublicationEligible)
	require.Equal(t, contract.CandidateRiskLow, evidence.Risk)
	require.Equal(t, 1, runner.calls)
	require.Len(t, evidence.Commands, 1)
	content, err := os.ReadFile(filepath.Join(
		checkout, "internal", "example", "fix.go",
	))
	require.NoError(t, err)
	require.Contains(t, string(content), "func fixed() bool")
}

func TestVerifyCandidateRejectsTestCommandTreeMutation(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	writeCandidateFile(t, checkout, "internal/example/fix.go", "package example\n")
	runner := &verificationRunnerStub{
		run: func() {
			writeCandidateFile(
				t,
				checkout,
				"internal/example/injected.go",
				"package example\n",
			)
		},
	}
	_, err := issueagentverify.VerifyCandidate(
		context.Background(),
		checkout,
		issueagentverify.CandidateSnapshot{
			SchemaVersion: 2,
			TaskID:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BaseSHA:       "0123456789abcdef0123456789abcdef01234567",
			ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
				Path:          "internal/example/fix.go",
				Operation:     contract.FileOperationUpsert,
				Mode:          contract.FileModeRegular,
				ContentBase64: contract.EncodeFileContent([]byte("package example\n\nfunc fixed() {}\n")),
			}}},
		},
		issueagentverify.VerificationPolicy{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			RequiredSuites: []string{"focused"},
			Commands: []issueagentverify.VerificationCommandPlan{{
				Arguments:  []string{"go", "test", "./internal/example"},
				WorkingDir: ".",
			}},
		},
		runner,
		time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	)
	require.EqualError(t, err, "verification command changed the candidate tree")
}

type verificationRunnerStub struct {
	calls int
	run   func()
}

func (runner *verificationRunnerStub) Run(
	context.Context,
	issueagentverify.VerificationCommandPlan,
) (issueagentverify.VerificationCommandResult, error) {
	runner.calls++
	if runner.run != nil {
		runner.run()
	}
	return issueagentverify.VerificationCommandResult{
		ExitCode: 0, Stdout: []byte("ok\n"),
		Duration: time.Millisecond,
	}, nil
}
