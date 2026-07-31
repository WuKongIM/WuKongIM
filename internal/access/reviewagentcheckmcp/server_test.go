package reviewagentcheckmcp_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	checkmcp "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcheckmcp"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestServerExposesOnlyNamedCheckTools(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner := testRunner(t)
	server, err := checkmcp.NewServer(checkmcp.Config{
		Runner: runner, Generation: generation(),
	})
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"check_list", "check_result", "check_run"},
		checkmcp.ToolNames(),
	)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test", Version: "v1"},
		nil,
	)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	var names []string
	for tool, toolErr := range clientSession.Tools(ctx, nil) {
		require.NoError(t, toolErr)
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	require.Equal(t, checkmcp.ToolNames(), names)

	list, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "check_list", Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.Contains(t, textContent(list), "go-unit")

	run, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "check_run", Arguments: map[string]any{"name": "go-unit"},
	})
	require.NoError(t, err)
	require.Contains(t, textContent(run), `"outcome":"passed"`)

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "check_result", Arguments: map[string]any{"name": "go-unit"},
	})
	require.NoError(t, err)
	require.Contains(t, textContent(result), `"name":"go-unit"`)
}

func TestServerRejectsCallerCommandOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, err := checkmcp.NewServer(checkmcp.Config{
		Runner: testRunner(t), Generation: generation(),
	})
	require.NoError(t, err)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test", Version: "v1"},
		nil,
	)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "check_run",
		Arguments: map[string]any{
			"name": "go-unit", "command": "curl metadata.google.internal",
		},
	})
	require.Error(t, err)
}

func testRunner(t *testing.T) *verify.Runner {
	t.Helper()
	root := t.TempDir()
	ledger, err := verify.NewFileLedger(
		filepath.Join(t.TempDir(), "ledger.jsonl"),
		root,
	)
	require.NoError(t, err)
	runner, err := verify.NewRunner(verify.RunnerConfig{
		WorkspaceRoot: root,
		Policy: verify.Policy{
			TrustedChecks: map[string]verify.CheckPlan{
				"go-unit": {
					Arguments:      []string{"go", "test"},
					WorkingDir:     ".",
					TimeoutSeconds: 30,
					MaxOutputBytes: 1 << 20,
				},
			},
		},
		Executor: passingExecutor{},
		Ledger:   ledger,
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
		},
	})
	require.NoError(t, err)
	return runner
}

type passingExecutor struct{}

func (passingExecutor) Execute(
	context.Context,
	verify.ProcessRequest,
) (verify.ProcessResult, error) {
	return verify.ProcessResult{
		ExitCode: 0, Stdout: []byte("ok"),
		Duration: time.Second,
	}, nil
}

func generation() contract.GenerationIdentity {
	return contract.GenerationIdentity{
		Repository: "WuKongIM/WuKongIM", PullRequest: 42,
		HeadSHA:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BaseSHA:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TestMergeSHA:   "cccccccccccccccccccccccccccccccccccccccc",
		IntentDigest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Generation:     1,
		StateParentSHA: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	}
}

func textContent(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return content.Text
}
