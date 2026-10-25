package presence

import (
	"errors"
	"fmt"
	"testing"
)

func TestActivationFailurePhaseIsClosedAndSelectsInnermostKnownPhase(t *testing.T) {
	original := errors.New("original cause")
	inner := WrapActivationPhase(ActivationPhaseRecovery, original)
	outer := WrapActivationPhase(ActivationPhaseRegister, fmt.Errorf("transport boundary: %w", inner))
	if !errors.Is(outer, original) || ActivationFailurePhase(outer) != ActivationPhaseRecovery || ActivationFailurePhase(outer).String() != "recovery" {
		t.Fatalf("nested phase/cause = %q / %v", ActivationFailurePhase(outer).String(), outer)
	}
	for _, phase := range []ActivationPhase{ActivationPhaseTarget, ActivationPhaseLocalRegister, ActivationPhaseRegister, ActivationPhaseAction, ActivationPhaseCommit, ActivationPhaseLocalRecheck, ActivationPhaseRecovery, ActivationPhaseDirectoryRegister, ActivationPhaseDirectoryCommit} {
		err := WrapActivationPhase(phase, original)
		if !errors.Is(err, original) || ActivationFailurePhase(err) != phase || phase.String() == "" {
			t.Fatalf("phase/cause = %q / %v", phase.String(), err)
		}
	}
	unknown := ActivationPhase("private-uid")
	if unknown.String() != "" || WrapActivationPhase(unknown, original) != original || ActivationFailurePhase(original).String() != "" {
		t.Fatal("unknown phase accepted or unclassified error invented a phase")
	}
	if WrapActivationPhase(ActivationPhaseRegister, nil) != nil || ActivationFailurePhase(nil).String() != "" {
		t.Fatal("nil error acquired a phase")
	}
}
