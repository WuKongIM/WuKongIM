package issueagentmodel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCodexUsageAcceptsOnlyAuthoritativeTurnCompletion(t *testing.T) {
	t.Parallel()

	events := []byte(
		"{\"type\":\"item.completed\",\"usage\":{\"input_tokens\":999,\"output_tokens\":999}}\n" +
			"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":25,\"cached_input_tokens\":20,\"output_tokens\":7,\"reasoning_output_tokens\":3}}\n",
	)
	input, output := parseCodexUsage(events)
	require.Equal(t, uint64(25), input)
	require.Equal(t, uint64(7), output)
}
