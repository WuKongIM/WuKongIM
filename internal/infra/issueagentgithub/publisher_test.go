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
		AllowedPaths:   []string{"pkg", "test/e2e/issue_agent"},
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
				Mode:      issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("{}")),
			},
		},
		{
			name: "existing AGENTS",
			file: issueagent.FileChange{
				Path: "test/e2e/AGENTS.md", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("changed")),
			},
		},
		{
			name: "executable",
			file: issueagent.FileChange{
				Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeExecutable, ContentBase64: issueagent.EncodeFileContent([]byte("fix")),
			},
		},
		{
			name: "outside allowed path",
			file: issueagent.FileChange{
				Path: "docs/fix.md", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("fix")),
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
				Mode: issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("package example\n")),
			},
			{
				Path:          "test/e2e/issue_agent/issue_42/AGENTS.md",
				Operation:     issueagent.FileOperationUpsert,
				Mode:          issueagent.FileModeRegular,
				ContentBase64: issueagent.EncodeFileContent(template),
			},
		}},
		Limits: issueagent.ChangeSetLimits{
			MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDeletions: 2,
		},
		ProtectedPaths:              []string{".github/issue-agent", "AGENTS.md", "cmd/wkissueagent"},
		AllowedPaths:                []string{"pkg", "test/e2e/issue_agent"},
		ScenarioInstructionTemplate: template,
	}
	require.NoError(t, issueagentgithub.ValidatePublish(input))
}

func TestPublisherInjectsExactTrustedScenarioInstructions(t *testing.T) {
	t.Parallel()

	template := []byte("# Trusted scenario instructions\n")
	changeSet, err := issueagentgithub.InjectScenarioInstructions(
		issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:          "test/e2e/issue_agent/issue_42/reproduction_test.go",
			Operation:     issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: issueagent.EncodeFileContent([]byte("package issue_42\n")),
		}}},
		42,
		template,
	)
	require.NoError(t, err)
	require.Len(t, changeSet.Files, 2)
	require.Equal(t, "test/e2e/issue_agent/issue_42/AGENTS.md", changeSet.Files[0].Path)
	content, err := issueagent.DecodeFileContent(changeSet.Files[0])
	require.NoError(t, err)
	require.Equal(t, template, content)

	withoutInstructions := issueagentgithub.PublishValidation{
		IssueNumber: 42, Branch: "agent/issue-42", BaseBranch: "main",
		ExpectedParentSHA: fortyHex("a"),
		ChangeSet:         issueagent.ChangeSet{Files: changeSet.Files[1:]},
		Limits: issueagent.ChangeSetLimits{
			MaxFiles: 2, MaxFileBytes: 1024, MaxTotalBytes: 4096,
		},
		ProtectedPaths:              []string{".github/issue-agent"},
		AllowedPaths:                []string{"test/e2e/issue_agent"},
		ScenarioInstructionTemplate: template,
	}
	require.ErrorContains(
		t, issueagentgithub.ValidatePublish(withoutInstructions),
		"trusted scenario instruction file is missing",
	)
}

func TestPublisherReplacesModelAuthoredScenarioInstructions(t *testing.T) {
	t.Parallel()

	template := []byte("# Trusted\n")
	changeSet, err := issueagentgithub.InjectScenarioInstructions(
		issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path:          "test/e2e/issue_agent/issue_42/AGENTS.md",
			Operation:     issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: issueagent.EncodeFileContent([]byte("# Untrusted\n")),
		}}},
		42,
		template,
	)
	require.NoError(t, err)
	require.Len(t, changeSet.Files, 1)
	content, err := issueagent.DecodeFileContent(changeSet.Files[0])
	require.NoError(t, err)
	require.Equal(t, template, content)
}

func TestPublisherValidationRejectsFrozenReproductionAndProtectedFilePrefixes(t *testing.T) {
	t.Parallel()

	for _, filePath := range []string{
		"test/e2e/issue_agent/issue_42/reproduction_test.go",
		"test/e2e/issue_agent/issue_42/helper.go",
		"internal/app/issue_agent.go",
		"scripts/issue_agent_schema_test.go",
	} {
		input := issueagentgithub.PublishValidation{
			IssueNumber: 42, Branch: "agent/issue-42", BaseBranch: "main",
			ExpectedParentSHA: fortyHex("a"),
			ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
				Path: filePath, Operation: issueagent.FileOperationUpsert,
				Mode:          issueagent.FileModeRegular,
				ContentBase64: issueagent.EncodeFileContent([]byte("changed")),
			}}},
			Limits: issueagent.ChangeSetLimits{
				MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDeletions: 2,
			},
			ProtectedPaths: []string{"internal/app/issue_agent", "scripts/issue_agent"},
			AllowedPaths:   []string{"internal", "scripts", "test/e2e"},
			ImmutablePaths: []string{"test/e2e/issue_agent/issue_42"},
		}
		require.Error(t, issueagentgithub.ValidatePublish(input), filePath)
	}
}
