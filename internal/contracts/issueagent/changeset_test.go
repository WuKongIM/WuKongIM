package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestChangeSetAcceptsBoundedSortedRegularFiles(t *testing.T) {
	t.Parallel()

	changeSet := issueagent.ChangeSet{
		Files: []issueagent.FileChange{
			{
				Path:      "internal/usecase/example/app.go",
				Operation: issueagent.FileOperationUpsert,
				Mode:      issueagent.FileModeRegular,
				Content:   []byte("package example\n"),
			},
			{
				Path:      "test/e2e/message/example/example_test.go",
				Operation: issueagent.FileOperationDelete,
			},
		},
	}

	require.NoError(t, issueagent.ValidateChangeSet(changeSet, issueagent.ChangeSetLimits{
		MaxFiles:      3,
		MaxFileBytes:  1024,
		MaxTotalBytes: 2048,
		MaxDeletions:  1,
	}))
}

func TestChangeSetRejectsUnsafeRepositoryPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []issueagent.FileChange
	}{
		{
			name: "traversal",
			files: []issueagent.FileChange{{
				Path: "../AGENTS.md", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, Content: []byte("unsafe"),
			}},
		},
		{
			name: "absolute",
			files: []issueagent.FileChange{{
				Path: "/tmp/payload", Operation: issueagent.FileOperationUpsert,
				Mode: issueagent.FileModeRegular, Content: []byte("unsafe"),
			}},
		},
		{
			name: "case collision",
			files: []issueagent.FileChange{
				{
					Path: "internal/A.go", Operation: issueagent.FileOperationUpsert,
					Mode: issueagent.FileModeRegular, Content: []byte("a"),
				},
				{
					Path: "internal/a.go", Operation: issueagent.FileOperationUpsert,
					Mode: issueagent.FileModeRegular, Content: []byte("b"),
				},
			},
		},
		{
			name: "unsorted",
			files: []issueagent.FileChange{
				{
					Path: "z.go", Operation: issueagent.FileOperationUpsert,
					Mode: issueagent.FileModeRegular, Content: []byte("z"),
				},
				{
					Path: "a.go", Operation: issueagent.FileOperationUpsert,
					Mode: issueagent.FileModeRegular, Content: []byte("a"),
				},
			},
		},
		{
			name: "delete with content",
			files: []issueagent.FileChange{{
				Path: "obsolete.go", Operation: issueagent.FileOperationDelete,
				Content: []byte("smuggled"),
			}},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := issueagent.ValidateChangeSet(
				issueagent.ChangeSet{Files: test.files},
				issueagent.ChangeSetLimits{
					MaxFiles:      4,
					MaxFileBytes:  1024,
					MaxTotalBytes: 2048,
					MaxDeletions:  2,
				},
			)
			require.Error(t, err)
		})
	}
}
