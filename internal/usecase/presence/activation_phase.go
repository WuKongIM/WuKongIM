package presence

import (
	"errors"
	"strings"
)

// ActivationPhase is the closed, identity-free activation failure vocabulary.
// It is diagnostic evidence only and never changes routing or retry decisions.
type ActivationPhase string

const (
	ActivationPhaseTarget             ActivationPhase = "target"
	ActivationPhaseLocalRegister      ActivationPhase = "local_register"
	ActivationPhaseRegister           ActivationPhase = "register"
	ActivationPhaseAction             ActivationPhase = "action"
	ActivationPhaseCommit             ActivationPhase = "commit"
	ActivationPhaseLocalRecheck       ActivationPhase = "local_recheck"
	ActivationPhaseRecovery           ActivationPhase = "recovery"
	ActivationPhaseDirectoryRegister  ActivationPhase = "directory_register"
	ActivationPhaseDirectoryCommit    ActivationPhase = "directory_commit"
	ActivationPhaseRecoveryAdmission  ActivationPhase = "recovery_admission"
	ActivationPhaseRecoveryProvider   ActivationPhase = "recovery_provider"
	ActivationPhaseRecoveryValidation ActivationPhase = "recovery_validation"
	ActivationPhaseRecoveryInstall    ActivationPhase = "recovery_install"
	ActivationPhaseRecoveryReadback   ActivationPhase = "recovery_readback"
)

// String returns a fixed log value; unknown phases cannot carry arbitrary text.
func (p ActivationPhase) String() string {
	switch p {
	case ActivationPhaseTarget, ActivationPhaseLocalRegister, ActivationPhaseRegister, ActivationPhaseAction, ActivationPhaseCommit, ActivationPhaseLocalRecheck, ActivationPhaseRecovery, ActivationPhaseDirectoryRegister, ActivationPhaseDirectoryCommit,
		ActivationPhaseRecoveryAdmission, ActivationPhaseRecoveryProvider, ActivationPhaseRecoveryValidation, ActivationPhaseRecoveryInstall, ActivationPhaseRecoveryReadback:
		return string(p)
	default:
		return ""
	}
}

type activationPhaseError struct {
	// phase is always one known activation stage and contains no request data.
	phase ActivationPhase
	// err retains the original error identity for errors.Is and errors.As.
	err error
}

func (e *activationPhaseError) Error() string {
	return "presence activation " + strings.ReplaceAll(e.phase.String(), "_", " ") + ": " + e.err.Error()
}
func (e *activationPhaseError) Unwrap() error { return e.err }

// WrapActivationPhase annotates one failure without changing its original cause.
// Successful and unknown-phase inputs preserve their exact prior behavior.
func WrapActivationPhase(phase ActivationPhase, err error) error {
	if err == nil || phase.String() == "" {
		return err
	}
	return &activationPhaseError{phase: phase, err: err}
}

// ActivationFailurePhase returns the innermost known activation phase. It never
// exposes the cause text or pretends that a remote status retained source phases.
func ActivationFailurePhase(err error) ActivationPhase {
	var found ActivationPhase
	for err != nil {
		var phaseErr *activationPhaseError
		if !errors.As(err, &phaseErr) {
			break
		}
		found = phaseErr.phase
		err = phaseErr.err
	}
	return found
}
