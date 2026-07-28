package issueagentworker_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

func TestEvidenceSanitizerRemovesCredentialShapesAndBoundsText(t *testing.T) {
	t.Parallel()

	input := "Authorization: Bearer secret-value\n" +
		"GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz1234567890\n" +
		"safe tail"
	sanitized, truncated := issueagentworker.SanitizeText(input, 30)
	require.True(t, truncated)
	require.NotContains(t, sanitized, "secret-value")
	require.NotContains(t, sanitized, "ghp_")
	require.LessOrEqual(t, len(sanitized), 30)
}
