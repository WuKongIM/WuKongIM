package reviewagentcli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
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

func TestReviewAgentCLIDecodesWorkflowRiskSelection(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		context.Background(),
		[]string{"build-context"},
		strings.NewReader(`{
			"pull_request":716,
			"generation":{
				"repository":"WuKongIM/WuKongIM",
				"pull_request":716,
				"head_sha":"1111111111111111111111111111111111111111",
				"base_sha":"2222222222222222222222222222222222222222",
				"test_merge_sha":"3333333333333333333333333333333333333333",
				"intent_digest":"sha256:4444444444444444444444444444444444444444444444444444444444444444",
				"generation":1,
				"state_parent_sha":"5555555555555555555555555555555555555555"
			},
			"review_reason":"review requested",
			"prior_findings":null,
			"risk":{
				"race":true,
				"integration":true,
				"e2e":true,
				"three_node_cluster":true
			}
		}`),
		&stdout,
		&stderr,
		cli.Operations{
			BuildContext: func(
				_ context.Context,
				request cli.BuildContextRequest,
			) (cli.BuildContextResponse, error) {
				require.Equal(t, verify.RiskSelection{
					Race: true, Integration: true,
					E2E: true, ThreeNodeCluster: true,
				}, request.Risk)
				return cli.BuildContextResponse{}, nil
			},
		},
	)
	require.Zero(t, code)
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
