package issueagentworker_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceToolsAreBoundedDeterministicAndFenced(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg", "example"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "pkg", "example", "a.go"),
		[]byte("package example\n\nconst old = true\n"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "pkg", "example", "b.go"),
		[]byte("package example\n"), 0o644,
	))
	broker, err := issueagentworker.NewBroker(issueagentworker.BrokerConfig{
		Workspace: root, AllowedWritePaths: []string{"pkg/example"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 4,
		}},
		MaxFileBytes: 1024, MaxOutputBytes: 1024,
	}, &fakeRunner{})
	require.NoError(t, err)

	listed, err := broker.List(context.Background(), "pkg/example", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"pkg/example/a.go", "pkg/example/b.go"}, listed.Paths)
	found, err := broker.Search(context.Background(), "const old", ".", 10)
	require.NoError(t, err)
	require.Len(t, found.Matches, 1)
	require.Equal(t, "pkg/example/a.go", found.Matches[0].Path)

	before, err := broker.Read(context.Background(), "pkg/example/a.go")
	require.NoError(t, err)
	written, err := broker.Apply(context.Background(), issueagentworker.ApplyRequest{
		Path: "pkg/example/a.go", ExpectedSHA256: before.SHA256,
		ContentBase64: issueagent.EncodeFileContent(
			[]byte("package example\n\nconst old = false\n"),
		),
	})
	require.NoError(t, err)
	require.NotEqual(t, before.SHA256, written.SHA256)

	_, err = broker.Apply(context.Background(), issueagentworker.ApplyRequest{
		Path: "pkg/example/a.go", ExpectedSHA256: before.SHA256,
		ContentBase64: issueagent.EncodeFileContent([]byte("stale")),
	})
	require.Error(t, err)
	_, err = broker.Apply(context.Background(), issueagentworker.ApplyRequest{
		Path:          "docs/outside.md",
		ContentBase64: issueagent.EncodeFileContent([]byte("outside")),
	})
	require.Error(t, err)
}
