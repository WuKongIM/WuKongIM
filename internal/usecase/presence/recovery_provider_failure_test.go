package presence

import (
	"context"
	"errors"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

type recoveryProviderTestCause struct{}

func (*recoveryProviderTestCause) Error() string { return "private-cause-sentinel" }

func TestRecoveryProviderFailureEvidencePreservesCauseAndReturnsValues(t *testing.T) {
	cause := &recoveryProviderTestCause{}
	evidence := RecoveryProviderFailureEvidence{Reason: RecoveryProviderSnapshotBefore, Calls: 3, FailureElapsed: time.Millisecond}
	err := WithRecoveryProviderFailure(cause, evidence)
	if err == cause || err.Error() != cause.Error() || !errors.Is(err, cause) {
		t.Fatal("failure capsule changed the original error contract")
	}
	var typed *recoveryProviderTestCause
	if !errors.As(err, &typed) || typed != cause {
		t.Fatal("failure capsule hid original typed cause")
	}
	got, ok := RecoveryProviderFailure(err)
	if !ok || got != evidence {
		t.Fatalf("failure evidence=%+v/%t", got, ok)
	}
	got.Calls = 16
	again, _ := RecoveryProviderFailure(err)
	if again.Calls != 3 {
		t.Fatal("extractor exposed mutable shared observation")
	}
	if WithRecoveryProviderFailure(nil, evidence) != nil || WithRecoveryProviderFailure(cause, RecoveryProviderFailureEvidence{}) != cause {
		t.Fatal("success or unknown evidence changed prior behavior")
	}
}

func TestRecoveryProviderFailureMetadataKeepsExistingContextNormalization(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
			directory.BecomeAuthority(target)
			data := RecoveryProviderFailureEvidence{Reason: RecoveryProviderContext, Calls: 2, FailureElapsed: time.Millisecond}
			owners := recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
				return nil, WithRecoveryProviderFailure(cause, data)
			})
			_, err := NewAuthorityRecovery(directory, owners).Endpoints(context.Background(), target, []string{"fixture"})
			got, ok := RecoveryProviderFailure(err)
			if !ok || got != data {
				t.Fatalf("normalization lost observation: %+v/%t", got, ok)
			}
			if !errors.Is(err, authority.ErrRouteNotReady) || errors.Is(err, cause) || ActivationFailurePhase(err) != ActivationPhaseRecoveryProvider {
				t.Fatalf("normalization family/phase changed: %v", err)
			}
		})
	}
}

func TestRecoveryProviderFailureEvidenceRejectsUnknownButRetainsMeasuredZeros(t *testing.T) {
	cause := errors.New("private-sentinel")
	data := RecoveryProviderFailureEvidence{Reason: RecoveryProviderInvalidInput, Calls: 17, OwnersPlanned: 257}
	for _, invalid := range []RecoveryProviderFailureEvidence{
		{Reason: RecoveryProviderReason(255)},
		{Reason: RecoveryProviderInvalidInput, Observed: 1 << 15},
		{Reason: RecoveryProviderInvalidInput, FailureElapsed: -1},
		{Reason: RecoveryProviderInvalidInput, OwnerMode: RecoveryProviderOwnerMode(255)},
		{Reason: RecoveryProviderInvalidInput, OwnerError: RecoveryProviderOwnerError(255)},
	} {
		if WithRecoveryProviderFailure(cause, invalid) != cause {
			t.Fatal("unknown diagnostic input was accepted")
		}
	}
	if _, ok := RecoveryProviderFailure(WithRecoveryProviderFailure(cause, data)); !ok {
		t.Fatal("business-invalid counts hid diagnostic failure")
	}
	data.Observed = RecoveryProviderAdmissionStage.Mask()
	known, ok := RecoveryProviderFailure(WithRecoveryProviderFailure(cause, data))
	if !ok || known.Observed&RecoveryProviderAdmissionStage.Mask() == 0 || known.Stages[RecoveryProviderAdmissionStage] != 0 {
		t.Fatal("observed zero was confused with unknown")
	}
	data.Observed = 0
	unknown, _ := RecoveryProviderFailure(WithRecoveryProviderFailure(cause, data))
	if unknown.Observed == known.Observed {
		t.Fatal("missing stage was invented")
	}
}

type recoveryProviderCancelOwners struct {
	read func(context.Context, []RecoveryCall) (OwnedRecoveryResults, error)
}

func (*recoveryProviderCancelOwners) SupportsBoundedRecoveryResults() bool { return true }
func (*recoveryProviderCancelOwners) RecoverRoutes(context.Context, RouteTarget, []string) ([]Route, error) {
	panic("unexpected legacy")
}
func (o *recoveryProviderCancelOwners) RecoverRouteCallsBounded(ctx context.Context, calls []RecoveryCall, _ *RecoveryResultBudget, _ func(RecoveryResultUsage)) (OwnedRecoveryResults, error) {
	return o.read(ctx, calls)
}

func TestRecoveryProviderEvidenceSurvivesPhysicalContextPriority(t *testing.T) {
	for _, mode := range []string{"global", "group", "misaligned", "other_group"} {
		t.Run(mode, func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
			directory.BecomeAuthority(target)
			original := RecoveryProviderFailureEvidence{Reason: RecoveryProviderOwnerUnavailable, Calls: 1, FailureElapsed: time.Millisecond}
			owners := &recoveryProviderCancelOwners{}
			r := NewAuthorityRecovery(directory, owners)
			owners.read = func(ctx context.Context, calls []RecoveryCall) (OwnedRecoveryResults, error) {
				// Inject cancellation of the physical execution, independently of
				// its still-live public caller; no elapsed-time race is required.
				r.mu.Lock()
				for e := range r.active {
					e.cancel()
				}
				r.mu.Unlock()
				if ctx.Err() == nil {
					t.Error("physical cancellation not delivered")
				}
				cause := WithRecoveryProviderFailure(authority.ErrRouteNotReady, original)
				if mode == "global" {
					return OwnedRecoveryResults{}, cause
				}
				if mode == "misaligned" {
					return OwnedRecoveryResults{Results: [][]EndpointLookupResult{{{Err: cause}, {Err: cause}}}}, nil
				}
				if mode == "other_group" {
					return OwnedRecoveryResults{Results: [][]EndpointLookupResult{{{}, {Err: cause}}}}, nil
				}
				return OwnedRecoveryResults{Results: [][]EndpointLookupResult{{{Err: cause}}}}, nil
			}
			groups := []EndpointLookupGroup{{Target: target, UIDs: []string{"fixture"}}}
			if mode == "other_group" {
				groups = append(groups, EndpointLookupGroup{Target: target, UIDs: []string{"fixture-other"}})
			}
			results := r.EndpointsByTargets(context.Background(), groups)
			for i, result := range results {
				data, ok := RecoveryProviderFailure(result.Err)
				want := mode != "misaligned" && (mode != "other_group" || i == 1)
				if ok != want {
					t.Fatalf("%d observation=%t want=%t", i, ok, want)
				}
				if want {
					if !data.ContextOverride {
						t.Fatal("context priority not marked")
					}
					data.ContextOverride = false
					if data != original {
						t.Fatalf("frozen observation changed: %+v", data)
					}
				}
				if !errors.Is(result.Err, authority.ErrRouteNotReady) || errors.Is(result.Err, context.Canceled) {
					t.Fatal("historical context normalization changed")
				}
			}
		})
	}
}
