package reviewagent

import (
	"errors"
	"fmt"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// RenderStatus deterministically renders signed state without invoking a
// model or trusting mutable GitHub projections.
func RenderStatus(state contract.ReviewState, now time.Time) (string, error) {
	if err := contract.ValidateReviewState(state); err != nil {
		return "", err
	}
	if now.IsZero() || now.Location() != time.UTC ||
		now.Before(state.UpdatedAt) {
		return "", errors.New("invalid Review status time")
	}
	elapsed := now.Sub(state.UpdatedAt).Round(time.Second)
	return fmt.Sprintf(
		"<!-- review-agent-status:pr-%d -->\n"+
			"## Review Agent\n\n"+
			"- state: `%s`\n"+
			"- generation %d\n"+
			"- head: `%s`\n"+
			"- elapsed since state update: `%s`\n"+
			"- reconsiderations: %d/2\n"+
			"- explanations: %d/3\n",
		state.Generation.PullRequest,
		state.Phase,
		state.Generation.Generation,
		state.Generation.HeadSHA,
		elapsed,
		state.Budget.ReconsiderationsUsed,
		state.Budget.ExplanationsUsed,
	), nil
}
