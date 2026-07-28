package issueagent

import (
	"errors"
	"reflect"
)

// ValidateCheckpointSuccessor enforces immutable and monotonic state carried
// between two signed checkpoints. Signature validity alone is insufficient:
// a trusted Publisher must not accidentally rewrite already accepted facts.
func ValidateCheckpointSuccessor(previous, next Checkpoint) error {
	if err := ValidateCheckpoint(previous); err != nil {
		return errors.New("previous checkpoint is invalid")
	}
	if err := ValidateCheckpoint(next); err != nil {
		return errors.New("next checkpoint is invalid")
	}
	if previous.Repository != next.Repository ||
		previous.IssueNumber != next.IssueNumber ||
		next.Sequence != previous.Sequence+1 ||
		next.Generation < previous.Generation ||
		next.Generation > previous.Generation+1 {
		return errors.New("checkpoint successor identity is invalid")
	}
	if next.Generation == previous.Generation+1 {
		if next.Control != nil &&
			(previous.Control == nil ||
				next.Control.EventID != previous.Control.EventID) {
			switch next.Control.Kind {
			case "revise":
				if next.State == StateAuthorized &&
					next.FrozenInput.AuthorizationEvent == next.Control.EventID {
					return nil
				}
			case "cancel":
				if next.State == StateCancelled && next.Lease == nil &&
					commandDomainPreserved(previous, next, true) {
					return nil
				}
			case "address_review":
				if next.State == StateFixing && next.Lease != nil &&
					next.Lease.Phase == PhaseAddressReview &&
					next.Validation == nil &&
					commandDomainPreserved(previous, next, false) {
					return nil
				}
			case "adopt_head":
				if next.State == StateValidating && next.Work != nil &&
					next.Work.HeadSHA == next.Control.AdoptedHeadSHA &&
					adoptDomainPreserved(previous, next) {
					return nil
				}
			case "backport":
				if previous.State == StateMerged && next.State == StateMerged &&
					commandDomainPreserved(previous, next, true) {
					return nil
				}
			case "recover_chain":
				if next.Control.RecoveryAnchorCommentID > 0 &&
					next.Control.RecoveryAnchorDigest != "" &&
					next.Lease == nil && next.Model == nil &&
					recoveryBoundaryPreserved(previous, next) &&
					commandDomainPreserved(previous, next, true) {
					return nil
				}
			}
		}
		if next.State == StateAuthorized &&
			next.FrozenInput.AuthorizationEvent != previous.FrozenInput.AuthorizationEvent {
			return nil
		}
		if next.State == StateDiagnosed && previous.State == StateDiagnosed &&
			next.Diagnosis != nil &&
			next.Diagnosis.AuthorizationEvent != "" &&
			(previous.Diagnosis == nil ||
				next.Diagnosis.AuthorizationEvent != previous.Diagnosis.AuthorizationEvent) {
			return nil
		}
		return errors.New("new generation lacks a fresh authorization boundary")
	}
	if err := ValidateTransition(previous.State, next.State); err != nil {
		return err
	}
	if !reflect.DeepEqual(previous.FrozenInput, next.FrozenInput) {
		return errors.New("frozen Issue input changed within a generation")
	}
	if !reflect.DeepEqual(previous.Control, next.Control) {
		return errors.New("maintainer control audit changed within a generation")
	}
	if previous.Versions.ReportedRef != next.Versions.ReportedRef ||
		previous.Versions.DiagnosisBaseSHA != next.Versions.DiagnosisBaseSHA {
		return errors.New("frozen version baseline changed within a generation")
	}
	if previous.Versions.AffectedSHA == "" {
		if previous.State != StateAuthorized || next.State != StateVersionPinned ||
			next.Versions.AffectedSHA == "" {
			return errors.New("affected SHA was populated outside version pinning")
		}
	} else if previous.Versions.AffectedSHA != next.Versions.AffectedSHA {
		return errors.New("affected SHA changed within a generation")
	}
	if previous.Reproduction != nil &&
		!reflect.DeepEqual(previous.Reproduction, next.Reproduction) {
		return errors.New("frozen reproduction evidence changed within a generation")
	}
	if previous.Reproduction == nil && next.Reproduction != nil &&
		next.State != StateReproduced && next.State != StateAlreadyFixed {
		return errors.New("reproduction evidence appeared in an invalid state")
	}
	if err := validateWorkSuccessor(previous.Work, next.Work); err != nil {
		return err
	}
	if previous.Diagnosis != nil &&
		!reflect.DeepEqual(previous.Diagnosis, next.Diagnosis) {
		return errors.New("diagnosis changed within a generation")
	}
	if previous.Diagnosis == nil && next.Diagnosis != nil &&
		next.State != StateDiagnosed {
		return errors.New("diagnosis appeared in an invalid state")
	}
	if !budgetNondecreasing(previous.Budget, next.Budget) {
		return errors.New("checkpoint budget counters decreased")
	}
	return nil
}

func commandDomainPreserved(
	previous Checkpoint,
	next Checkpoint,
	preserveValidation bool,
) bool {
	if !reflect.DeepEqual(previous.FrozenInput, next.FrozenInput) ||
		!reflect.DeepEqual(previous.Versions, next.Versions) ||
		!reflect.DeepEqual(previous.Reproduction, next.Reproduction) ||
		!reflect.DeepEqual(previous.Work, next.Work) ||
		!reflect.DeepEqual(previous.Diagnosis, next.Diagnosis) ||
		!budgetNondecreasing(previous.Budget, next.Budget) {
		return false
	}
	return !preserveValidation ||
		reflect.DeepEqual(previous.Validation, next.Validation)
}

func recoveryBoundaryPreserved(previous Checkpoint, next Checkpoint) bool {
	expectedState := previous.State
	expectedAction := previous.NextAction
	switch previous.State {
	case StateReproducing:
		expectedState = StateVersionPinned
		expectedAction = ActionReproduce
	case StateDiagnosing:
		expectedState = StateDraftPROpen
		expectedAction = ActionDiagnose
	case StateFixing:
		expectedState = StateDiagnosed
		expectedAction = ActionImplementFix
	}
	return next.State == expectedState && next.NextAction == expectedAction
}

func adoptDomainPreserved(previous Checkpoint, next Checkpoint) bool {
	if previous.Work == nil || next.Work == nil ||
		previous.Work.Branch != next.Work.Branch ||
		previous.Work.PRNumber != next.Work.PRNumber ||
		next.Validation != nil {
		return false
	}
	previousWithoutWork := previous
	nextWithoutWork := next
	previousWithoutWork.Work = nil
	nextWithoutWork.Work = nil
	previousWithoutWork.State = nextWithoutWork.State
	previousWithoutWork.Generation = nextWithoutWork.Generation
	previousWithoutWork.Sequence = nextWithoutWork.Sequence
	previousWithoutWork.ExpectedPreviousCheckpointID =
		nextWithoutWork.ExpectedPreviousCheckpointID
	previousWithoutWork.PreviousCheckpointSHA256 =
		nextWithoutWork.PreviousCheckpointSHA256
	previousWithoutWork.Lease = nextWithoutWork.Lease
	previousWithoutWork.Validation = nextWithoutWork.Validation
	previousWithoutWork.Model = nextWithoutWork.Model
	previousWithoutWork.Control = nextWithoutWork.Control
	previousWithoutWork.NextAction = nextWithoutWork.NextAction
	return reflect.DeepEqual(previousWithoutWork, nextWithoutWork) &&
		budgetNondecreasing(previous.Budget, next.Budget)
}

func validateWorkSuccessor(previous, next *Work) error {
	if previous == nil {
		if next != nil && next.PRNumber > 0 {
			return errors.New("pull request appeared before Agent work")
		}
		return nil
	}
	if next == nil || previous.Branch != next.Branch {
		return errors.New("Agent work branch changed or disappeared")
	}
	if previous.PRNumber > 0 && previous.PRNumber != next.PRNumber {
		return errors.New("Agent pull request identity changed")
	}
	return nil
}

func budgetNondecreasing(previous, next Budget) bool {
	return next.ReproductionAttempts >= previous.ReproductionAttempts &&
		next.RemediationAttempts >= previous.RemediationAttempts &&
		next.CIRepairAttempts >= previous.CIRepairAttempts &&
		next.InfrastructureRetries >= previous.InfrastructureRetries &&
		next.WorkerSeconds >= previous.WorkerSeconds
}
