package issueagentgithub_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestPublisherValidationRejectsProtectedAndAmbiguousChanges(t *testing.T) {
	t.Parallel()

	base := issueagentgithub.PublishValidation{
		IssueNumber: 42, Branch: "agent/issue-42", BaseBranch: "main",
		ExpectedParentSHA: fortyHex("a"),
		Limits: issueagent.ChangeSetLimits{
			MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDeletions: 2,
		},
		ProtectedPaths: []string{".github/issue-agent", "AGENTS.md", "cmd/wkissueagent"},
		AllowedPaths:   []string{"pkg", "test/e2e/scenarios"},
	}
	tests := []struct {
		name string
		file issueagent.FileChange
	}{
		{
			name: "protected path",
			file: issueagent.FileChange{
				Path:      ".github/issue-agent/policy.json",
				Operation: issueagent.FileOperationUpsert,
				Mode:      issueagent.FileModeRegular, Content: []byte("{}"),
			},
		},
		{
			name: "existing AGENTS",
			file: issueagent.FileChange{
				Path: "test/e2e/AGENTS.md", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, Content: []byte("changed"),
			},
		},
		{
			name: "executable",
			file: issueagent.FileChange{
				Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeExecutable, Content: []byte("fix"),
			},
		},
		{
			name: "outside allowed path",
			file: issueagent.FileChange{
				Path: "docs/fix.md", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, Content: []byte("fix"),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := base
			input.ChangeSet.Files = []issueagent.FileChange{test.file}
			if test.name == "existing AGENTS" {
				input.ExistingPaths = map[string]bool{"test/e2e/AGENTS.md": true}
			}
			require.Error(t, issueagentgithub.ValidatePublish(input))
		})
	}
}

func TestPublisherValidationAcceptsBoundedRegularFilesAndTrustedScenarioInstructions(t *testing.T) {
	t.Parallel()

	template := []byte("# Generated E2E scenario instructions\n")
	input := issueagentgithub.PublishValidation{
		IssueNumber: 42, Branch: "agent/issue-42", BaseBranch: "main",
		ExpectedParentSHA: fortyHex("a"),
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{
			{
				Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, Content: []byte("package example\n"),
			},
			{
				Path:      "test/e2e/scenarios/issue_42/AGENTS.md",
				Operation: issueagent.FileOperationUpsert,
				Mode:      issueagent.FileModeRegular, Content: template,
			},
		}},
		Limits: issueagent.ChangeSetLimits{
			MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDeletions: 2,
		},
		ProtectedPaths:              []string{".github/issue-agent", "AGENTS.md", "cmd/wkissueagent"},
		AllowedPaths:                []string{"pkg", "test/e2e/scenarios"},
		ScenarioInstructionTemplate: template,
	}
	require.NoError(t, issueagentgithub.ValidatePublish(input))
}
