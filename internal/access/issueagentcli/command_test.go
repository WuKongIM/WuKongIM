package issueagentcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	"github.com/stretchr/testify/require"
)

func TestCommandRoutesEachV2OperationThroughStrictJSON(t *testing.T) {
	t.Parallel()

	called := ""
	operations := issueagentcli.Operations{
		RecoverTask: func(
			_ context.Context,
			request issueagentcli.RecoverTaskRequest,
		) (any, error) {
			called = request.TaskID
			return map[string]bool{"valid": true}, nil
		},
	}
	input, err := json.Marshal(issueagentcli.RecoverTaskRequest{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
		TaskID:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
		ControlSHA:   "0123456789abcdef0123456789abcdef01234567",
		StateHeadSHA: "1111111111111111111111111111111111111111",
	})
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	exit := issueagentcli.Run(
		context.Background(),
		[]string{"recover-task"},
		bytes.NewReader(input),
		&stdout,
		&stderr,
		operations,
	)
	require.Zero(t, exit)
	require.Contains(t, called, "sha256:")
	require.JSONEq(t, `{"valid":true}`, stdout.String())
	require.Empty(t, stderr.String())
}

func TestCommandRejectsUnknownFieldsAndLegacyCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		command string
		input   string
		exit    int
	}{
		{
			command: "recover-task",
			input: `{"repository":"WuKongIM/WuKongIM","issue_number":42,` +
				`"task_id":"x","base_sha":"x","state_head_sha":"x",` +
				`"provider":"unsupported"}`,
			exit: 1,
		},
		{command: "run-worker", input: `{}`, exit: 2},
		{command: "publish-checkpoint", input: `{}`, exit: 2},
	} {
		var stdout, stderr bytes.Buffer
		exit := issueagentcli.Run(
			context.Background(),
			[]string{test.command},
			bytes.NewBufferString(test.input),
			&stdout,
			&stderr,
			issueagentcli.Operations{},
		)
		require.Equal(t, test.exit, exit)
		require.Empty(t, stdout.String())
		require.NotEmpty(t, stderr.String())
	}
}
