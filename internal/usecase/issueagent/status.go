package issueagent

import (
	"errors"
	"fmt"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

const issueStatusMarker = "<!-- wukongim-issue-agent-status -->"

// RenderIssueStatus projects durable state into one mutable Issue comment.
func RenderIssueStatus(state contract.IssueAgentState) (string, error) {
	if err := contract.ValidateIssueAgentState(state); err != nil {
		return "", errors.New("cannot render invalid Issue Agent state")
	}
	var message string
	switch state.State {
	case contract.IssueStateTriaging:
		message = "Analyzing — checking the report, scope, and available evidence."
	case contract.IssueStateWaitingForInformation:
		message = "Waiting for information — please provide the concrete facts requested below."
	case contract.IssueStateWaitingForAuthorization:
		message = "Waiting for maintainer authorization — comment `/agent fix`."
	case contract.IssueStateEngineering:
		message = "Engineering — reproducing, diagnosing, fixing, and testing this Issue in one bounded run."
	case contract.IssueStateDraft:
		message = "Waiting for Review — a complete Draft PR is open for a maintainer."
	case contract.IssueStateReviewing:
		message = "Engineering Review feedback — addressing the current trusted unresolved threads together."
	case contract.IssueStateReadyForReview:
		message = "Ready for Review — a human remains the only merge authority."
	case contract.IssueStateNeedsHuman:
		message = "Needs human attention — automatic work stopped with its current evidence preserved."
	case contract.IssueStateCompleted:
		message = "Completed — the linked repair was merged by a human."
	case contract.IssueStateCancelled:
		message = "Cancelled — automatic work has stopped."
	case contract.IssueStateTakenOver:
		message = "Taken over — a human now owns the repair branch and the Agent will not write it."
	default:
		return "", fmt.Errorf("cannot render Issue Agent state %q", state.State)
	}
	return issueStatusMarker + "\n### Issue Agent\n\n" + message + "\n", nil
}
