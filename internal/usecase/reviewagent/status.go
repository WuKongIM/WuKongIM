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
	elapsed := now.Sub(state.StartedAt).Round(time.Second)
	repositoryURL := "https://github.com/" + state.Generation.Repository
	return fmt.Sprintf(
		"<!-- review-agent-status:pr-%d -->\n"+
			"## Review Agent\n\n"+
			"- state: `%s`\n"+
			"- generation %d\n"+
			"- head: [`%s`](%s/commit/%s)\n"+
			"- base: [`%s`](%s/commit/%s)\n"+
			"- test merge: [`%s`](%s/commit/%s)\n"+
			"- generation elapsed: `%s`\n"+
			"- automatic reviews: %d/1\n"+
			"- reconsiderations: %d/2\n"+
			"- explanations: %d/3\n",
		state.Generation.PullRequest,
		state.Phase,
		state.Generation.Generation,
		state.Generation.HeadSHA,
		repositoryURL,
		state.Generation.HeadSHA,
		state.Generation.BaseSHA,
		repositoryURL,
		state.Generation.BaseSHA,
		state.Generation.TestMergeSHA,
		repositoryURL,
		state.Generation.TestMergeSHA,
		elapsed,
		state.Budget.AutomaticReviewsUsed,
		state.Budget.ReconsiderationsUsed,
		state.Budget.ExplanationsUsed,
	), nil
}
