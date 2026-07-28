package issueagent

import "fmt"

// ValidateTransition enforces the durable lifecycle graph within one generation.
func ValidateTransition(from, to State) error {
	if terminalState(from) {
		return fmt.Errorf("Issue Agent state %q is terminal", from)
	}
	if humanTerminalState(to) && executionState(from) {
		return nil
	}
	if to == StateReadyForHuman && executionState(from) {
		return nil
	}
	var allowed bool
	switch from {
	case StateAwaitingTriage:
		allowed = to == StateNeedsInfo || to == StateAuthorized
	case StateNeedsInfo:
		allowed = to == StateAuthorized
	case StateAuthorized:
		allowed = to == StateVersionPinned
	case StateVersionPinned:
		allowed = to == StateReproducing
	case StateReproducing:
		allowed = to == StateNeedsInfo || to == StateAlreadyFixed ||
			to == StateReproduced || to == StateVersionPinned ||
			to == StateReadyForHuman
	case StateReproduced:
		allowed = to == StateDraftPROpen
	case StateDraftPROpen:
		allowed = to == StateDiagnosing
	case StateDiagnosing:
		allowed = to == StateDiagnosed || to == StateReadyForHuman ||
			to == StateDraftPROpen
	case StateDiagnosed:
		allowed = to == StateFixing || to == StateReadyForHuman
	case StateFixing:
		allowed = to == StateValidating || to == StateAlreadyFixed ||
			to == StateReadyForHuman || to == StateDiagnosed
	case StateValidating:
		allowed = to == StateFixing || to == StateAlreadyFixed ||
			to == StateReadyForReview || to == StateReadyForHuman
	case StateReadyForReview:
		allowed = to == StateMerged || to == StateReadyForHuman
	}
	if !allowed {
		return fmt.Errorf("illegal Issue Agent transition %q -> %q", from, to)
	}
	return nil
}

func terminalState(state State) bool {
	switch state {
	case StateAlreadyFixed, StateMerged, StateCancelled, StateSuperseded,
		StateWontFix:
		return true
	default:
		return false
	}
}

func humanTerminalState(state State) bool {
	switch state {
	case StateCancelled, StateSuperseded, StateWontFix:
		return true
	default:
		return false
	}
}

func executionState(state State) bool {
	switch state {
	case StateAuthorized, StateVersionPinned, StateReproducing,
		StateReproduced, StateDraftPROpen, StateDiagnosing, StateDiagnosed,
		StateFixing, StateValidating, StateReadyForReview:
		return true
	default:
		return false
	}
}
