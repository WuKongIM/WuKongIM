package presence

import (
	"context"
	"errors"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

type recoveryPhaseCause struct{ cause error }

func (e *recoveryPhaseCause) Error() string { return "fixed recovery failure" }
func (e *recoveryPhaseCause) Unwrap() error { return e.cause }

type recoveryPhaseDirectory struct {
	*authority.Directory
	missingErr, installErr, readbackErr error
	reads                               int
}

func (d *recoveryPhaseDirectory) RecoveryUIDs(target RouteTarget, uids []string) ([]string, error) {
	if d.missingErr != nil {
		return nil, d.missingErr
	}
	return d.Directory.RecoveryUIDs(target, uids)
}

func (d *recoveryPhaseDirectory) InstallRecoveredRoutes(target RouteTarget, uids []string, rows []Route) error {
	if d.installErr != nil {
		return d.installErr
	}
	return d.Directory.InstallRecoveredRoutes(target, uids, rows)
}

func (d *recoveryPhaseDirectory) EndpointsByUIDs(target RouteTarget, uids []string) ([]Route, error) {
	d.reads++
	if d.reads > 1 && d.readbackErr != nil {
		return nil, d.readbackErr
	}
	return d.Directory.EndpointsByUIDs(target, uids)
}

func TestAuthorityRecoveryFailureSubphasesPreserveOriginalCause(t *testing.T) {
	for _, name := range []string{"admission", "provider", "validation", "install", "readback"} {
		t.Run(name, func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			cause := &recoveryPhaseCause{cause: authority.ErrRouteNotReady}
			directory := &recoveryPhaseDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})}
			directory.BecomeAuthority(target)
			owners := recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
				if name == "provider" {
					return nil, cause
				}
				return nil, nil
			})
			switch name {
			case "validation":
				directory.missingErr = cause
			case "install":
				directory.installErr = cause
			case "readback":
				directory.readbackErr = cause
			}
			var port RecoveryDirectory = directory
			if name == "admission" {
				port = nil
			}
			_, err := NewAuthorityRecovery(port, owners).Endpoints(context.Background(), target, []string{"bob"})
			if !errors.Is(err, authority.ErrRouteNotReady) || ActivationFailurePhase(err).String() != "recovery_"+name {
				t.Fatalf("recovery failure phase/cause: phase=%q err=%v", ActivationFailurePhase(err).String(), err)
			}
			if name != "admission" {
				var got *recoveryPhaseCause
				if !errors.Is(err, cause) || !errors.As(err, &got) || got != cause {
					t.Fatalf("original typed recovery cause changed: %v", err)
				}
			}
		})
	}
}

func TestAuthorityRecoveryProviderSubphaseKeepsExistingContextNormalization(t *testing.T) {
	for _, ownerErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(ownerErr.Error(), func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
			directory.BecomeAuthority(target)
			owners := recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) { return nil, ownerErr })
			_, err := NewAuthorityRecovery(directory, owners).Endpoints(context.Background(), target, []string{"bob"})
			if !errors.Is(err, authority.ErrRouteNotReady) || errors.Is(err, ownerErr) || ActivationFailurePhase(err).String() != "recovery_provider" {
				t.Fatalf("provider failure changed original normalization: phase=%q err=%v", ActivationFailurePhase(err).String(), err)
			}
		})
	}
}

func TestRecoveryFailureSubphaseVocabularyIsClosedAndInnermost(t *testing.T) {
	for _, value := range []string{"recovery_admission", "recovery_provider", "recovery_validation", "recovery_install", "recovery_readback"} {
		phase := ActivationPhase(value)
		cause := errors.New("original cause")
		err := WrapActivationPhase(ActivationPhaseRegister, WrapActivationPhase(ActivationPhaseRecovery, WrapActivationPhase(phase, cause)))
		if phase.String() != value || ActivationFailurePhase(err) != phase || !errors.Is(err, cause) {
			t.Fatalf("fixed subphase was lost: phase=%q err=%v", ActivationFailurePhase(err).String(), err)
		}
	}
	if phase := ActivationPhase("recovery_private-uid"); phase.String() != "" {
		t.Fatalf("arbitrary recovery subphase accepted: %q", phase.String())
	}
}
