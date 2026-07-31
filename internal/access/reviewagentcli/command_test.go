package reviewagentcli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
)

func TestReviewAgentCLIUsesOneStrictJSONDocument(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		context.Background(),
		[]string{"reconcile-github"},
		strings.NewReader(
			`{"pull_request":42,"signal_kind":"opened","run_id":9,"comment_id":0}`,
		),
		&stdout,
		&stderr,
		cli.Operations{
			ReconcileGitHub: func(
				_ context.Context,
				request cli.ReconcileGitHubRequest,
			) (cli.ReconcileGitHubResponse, error) {
				require.Equal(t, int64(42), request.PullRequest)
				return cli.ReconcileGitHubResponse{}, nil
			},
		},
	)
	require.Zero(t, code)
	require.Contains(t, stdout.String(), `"state_changed":false`)
	require.Empty(t, stderr.String())
}

func TestReviewAgentCLIRejectsFlagsUnknownFieldsAndTrailingInput(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		args  []string
		input string
	}{
		{[]string{"reconcile-github", "--pr=42"}, `{}`},
		{[]string{"unknown"}, `{}`},
		{[]string{"reconcile-github"}, `{"secret":"do-not-echo"}`},
		{[]string{"reconcile-github"}, `{} {}`},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := cli.Run(
			context.Background(),
			test.args,
			strings.NewReader(test.input),
			&stdout,
			&stderr,
			cli.Operations{},
		)
		require.Equal(t, 1, code)
		require.Empty(t, stdout.String())
		require.Equal(t, "review agent command failed\n", stderr.String())
		require.NotContains(t, stderr.String(), "do-not-echo")
	}
}
