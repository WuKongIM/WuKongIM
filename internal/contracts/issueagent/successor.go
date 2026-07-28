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
