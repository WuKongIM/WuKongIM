package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestParseIssueCommandAcceptsOnlyTheExactFirstLine(t *testing.T) {
	t.Parallel()

	command, ok := issueagent.ParseIssueCommand("/agent fix\nplease investigate")
	require.True(t, ok)
	require.Equal(t, issueagent.IssueCommandFix, command)

	_, ok = issueagent.ParseIssueCommand("please investigate\n/agent fix")
	require.False(t, ok)

	_, ok = issueagent.ParseIssueCommand(" /agent fix")
	require.False(t, ok)

	_, ok = issueagent.ParseIssueCommand("/agent approve-risk")
	require.False(t, ok)
}
