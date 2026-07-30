package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/stretchr/testify/require"
)

func TestIssueAgentCompositionExposesOnlyV2Roles(t *testing.T) {
	t.Parallel()

	operations := app.NewIssueAgentOperations(app.IssueAgentConfig{
		HTTPClient: &http.Client{}, APIBaseURL: "https://api.github.com",
		Repository: "WuKongIM/WuKongIM", AppLogin: "agent[bot]",
		WorkingDirectory: t.TempDir(), Now: time.Now,
	})
	require.NotNil(t, operations.ReconcileGitHub)
	require.NotNil(t, operations.RecoverTask)
	require.NotNil(t, operations.BuildContext)
	require.NotNil(t, operations.CaptureCandidate)
	require.NotNil(t, operations.VerifyCandidate)
	require.NotNil(t, operations.MintAppToken)
	require.NotNil(t, operations.PublishCandidate)
}
