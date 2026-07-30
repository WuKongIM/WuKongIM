package reviewagent_test

import (
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestExplanationResultIsGenerationBoundAndBounded(t *testing.T) {
	t.Parallel()

	result := contract.ExplanationResult{
		SchemaVersion: 1,
		Generation:    validGeneration(),
		Reply:         "The stale read permits two owners for the same slot.",
	}
	require.NoError(t, contract.ValidateExplanationResult(result))
	digest, err := contract.ExplanationResultDigest(result)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(digest, "sha256:"))

	result.Reply = strings.Repeat("x", (64<<10)+1)
	require.EqualError(
		t,
		contract.ValidateExplanationResult(result),
		"invalid Review explanation reply",
	)

	result.Reply = "Do not repeat <!-- review-agent-status:pr-42 -->"
	require.EqualError(
		t,
		contract.ValidateExplanationResult(result),
		"invalid Review explanation reply",
	)
}
