package reviewagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestRenderStatusUsesOnlySignedState(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseQueued
	state.Budget.ReconsiderationsUsed = 1
	state.Budget.ExplanationsUsed = 2

	body, err := reviewagent.RenderStatus(
		state,
		time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	for _, expected := range []string{
		"<!-- review-agent-status:pr-42 -->",
		"queued",
		"generation 1",
		strings.Repeat("a", 40),
		"reconsiderations: 1/2",
		"explanations: 2/3",
	} {
		require.Contains(t, body, expected)
	}
}
