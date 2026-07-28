package issueagent

import (
	"fmt"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ValidateTransition enforces the approved lifecycle graph for one generation.
func ValidateTransition(from, to issueagentcontract.State) error {
	if isTerminal(from) {
		return fmt.Errorf("Issue Agent state %q is terminal", from)
	}
	if isHumanTerminal(to) && isExecutionState(from) {
		return nil
	}

	var allowed bool
	switch from {
	case issueagentcontract.StateAwaitingTriage:
		allowed = to == issueagentcontract.StateNeedsInfo ||
			to == issueagentcontract.StateAuthorized
	case issueagentcontract.StateNeedsInfo:
		allowed = to == issueagentcontract.StateAuthorized
	case issueagentcontract.StateAuthorized:
		allowed = to == issueagentcontract.StateVersionPinned
	case issueagentcontract.StateVersionPinned:
		allowed = to == issueagentcontract.StateReproducing
	case issueagentcontract.StateReproducing:
		allowed = to == issueagentcontract.StateNeedsInfo ||
			to == issueagentcontract.StateAlreadyFixed ||
			to == issueagentcontract.StateReproduced
	case issueagentcontract.StateReproduced:
		allowed = to == issueagentcontract.StateDraftPROpen
	case issueagentcontract.StateDraftPROpen:
		allowed = to == issueagentcontract.StateDiagnosing
	case issueagentcontract.StateDiagnosing:
		allowed = to == issueagentcontract.StateDiagnosed ||
			to == issueagentcontract.StateReadyForHuman
	case issueagentcontract.StateDiagnosed:
		allowed = to == issueagentcontract.StateFixing ||
			to == issueagentcontract.StateReadyForHuman
	case issueagentcontract.StateFixing:
		allowed = to == issueagentcontract.StateValidating ||
			to == issueagentcontract.StateAlreadyFixed ||
			to == issueagentcontract.StateReadyForHuman
	case issueagentcontract.StateValidating:
		allowed = to == issueagentcontract.StateFixing ||
			to == issueagentcontract.StateAlreadyFixed ||
			to == issueagentcontract.StateReadyForReview ||
			to == issueagentcontract.StateReadyForHuman
	case issueagentcontract.StateReadyForReview:
		allowed = to == issueagentcontract.StateMerged ||
			to == issueagentcontract.StateReadyForHuman
	}
	if !allowed {
		return fmt.Errorf("illegal Issue Agent transition %q -> %q", from, to)
	}
	return nil
}

func isTerminal(state issueagentcontract.State) bool {
	switch state {
	case issueagentcontract.StateAlreadyFixed,
		issueagentcontract.StateMerged,
		issueagentcontract.StateCancelled,
		issueagentcontract.StateSuperseded,
		issueagentcontract.StateWontFix:
		return true
	default:
		return false
	}
}

func isHumanTerminal(state issueagentcontract.State) bool {
	switch state {
	case issueagentcontract.StateCancelled,
		issueagentcontract.StateSuperseded,
		issueagentcontract.StateWontFix:
		return true
	default:
		return false
	}
}

func isExecutionState(state issueagentcontract.State) bool {
	switch state {
	case issueagentcontract.StateAuthorized,
		issueagentcontract.StateVersionPinned,
		issueagentcontract.StateReproducing,
		issueagentcontract.StateReproduced,
		issueagentcontract.StateDraftPROpen,
		issueagentcontract.StateDiagnosing,
		issueagentcontract.StateDiagnosed,
		issueagentcontract.StateFixing,
		issueagentcontract.StateValidating,
		issueagentcontract.StateReadyForReview:
		return true
	default:
		return false
	}
}
