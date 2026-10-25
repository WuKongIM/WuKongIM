package gateway

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Exercise the existing returned activation error, not a private formatter call.
func TestActivationProviderFieldsPreserveClassMasksAndImmutableCapture(t *testing.T) {
	data := presence.RecoveryProviderFailureEvidence{
		Reason: presence.RecoveryProviderOwnerUnavailable, Calls: 3,
		FailureElapsed: 310 * time.Millisecond, ContextOverride: true,
		Observed: presence.RecoveryProviderAdmissionStage.Mask() | presence.RecoveryProviderOwnerReadStage.Mask() |
			presence.RecoveryProviderObservedOwnerBudget | presence.RecoveryProviderObservedOwnerDispatch |
			presence.RecoveryProviderObservedOwnerElapsed | presence.RecoveryProviderObservedOwnerError,
		OwnersPlanned: 3, OwnersCompleted: 1, OwnerNodeID: 2,
		OwnerMode: presence.RecoveryProviderOwnerRemoteCalls, OwnerError: presence.RecoveryProviderOwnerTimeout,
		PageStart: 1, OwnerConfiguredBudget: 250 * time.Millisecond,
		OwnerEffectiveBudget: 100 * time.Millisecond, OwnerElapsed: 300 * time.Millisecond,
	}
	data.Stages[presence.RecoveryProviderOwnerReadStage] = 300 * time.Millisecond
	cause := presence.WrapActivationPhase(presence.ActivationPhaseRegister,
		presence.WrapActivationPhase(presence.ActivationPhaseRecoveryProvider,
			presence.WithRecoveryProviderFailure(fmt.Errorf("private-cause: %w", authority.ErrRouteNotReady), data)))
	data.Calls = 99
	data.Stages[presence.RecoveryProviderOwnerReadStage] = time.Hour
	sess := session.New(session.Config{ID: 101})
	sess.SetValue(coregateway.SessionValueUID, "private-uid")
	handler := New(Options{Presence: &recordingPresence{activateErr: cause}})
	_, err := handler.OnSessionActivate(&coregateway.Context{Session: sess, RequestContext: context.Background()})
	if !errors.Is(err, authority.ErrRouteNotReady) || err.Error() != cause.Error() {
		t.Fatalf("changed activation error: %v", err)
	}
	if classified, ok := err.(interface{ GatewayAuthFailure() string }); !ok || classified.GatewayAuthFailure() != "activation_route_not_ready" {
		t.Fatal("changed activation failure class")
	}
	var provider interface{ GatewayAuthFailureLogFields() []wklog.Field }
	if !errors.As(fmt.Errorf("outer wrapper: %w", err), &provider) {
		t.Fatal("returned activation error has no optional immutable log metadata")
	}
	fields := provider.GatewayAuthFailureLogFields()
	wantKeys := []string{
		"recovery_provider_scope", "recovery_provider_reason", "recovery_provider_observed", "recovery_provider_failure_ns",
		"recovery_provider_context_override", "recovery_provider_calls", "recovery_provider_owners_planned", "recovery_provider_owner_scans_completed",
		"recovery_provider_admission_ns", "recovery_provider_owner_read_ns", "recovery_provider_owner_node",
		"recovery_provider_owner_configured_ns", "recovery_provider_page_start", "recovery_provider_owner_mode",
		"recovery_provider_owner_effective_ns", "recovery_provider_owner_elapsed_ns", "recovery_provider_owner_error",
	}
	keys := make([]string, len(fields))
	got := make(map[string]string)
	for i, field := range fields {
		keys[i] = field.Key
		got[field.Key] = fmt.Sprint(field.Value)
		for _, secret := range []string{"private-cause", "private-uid", "private-token", "private-device"} {
			if strings.Contains(got[field.Key], secret) {
				t.Fatalf("provider metadata contains identity/cause in %s", field.Key)
			}
		}
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("provider field order = %v, want %v", keys, wantKeys)
	}
	for key, want := range map[string]string{
		"recovery_provider_scope": "physical_round", "recovery_provider_calls": "3", "recovery_provider_admission_ns": "0",
		"recovery_provider_owner_read_ns": "300000000", "recovery_provider_owner_configured_ns": "250000000",
		"recovery_provider_owner_effective_ns": "100000000", "recovery_provider_owner_elapsed_ns": "300000000",
		"recovery_provider_owner_error": "timeout", "recovery_provider_context_override": "true",
	} {
		if got[key] != want {
			t.Errorf("%s=%q, want %q", key, got[key], want)
		}
	}
	fields[0].Value = "mutated-copy"
	if next := provider.GatewayAuthFailureLogFields(); len(next) != len(wantKeys) || next[0].Value != "physical_round" {
		t.Fatal("log projection exposed mutable retained fields")
	}
}

func TestActivationProviderFieldsAbsentRemainsUnknown(t *testing.T) {
	// This is also the wire decoder's plain route_not_ready status error.
	cause := authority.ErrRouteNotReady
	sess := session.New(session.Config{ID: 102})
	sess.SetValue(coregateway.SessionValueUID, "private-uid")
	handler := New(Options{Presence: &recordingPresence{activateErr: cause}})
	_, err := handler.OnSessionActivate(&coregateway.Context{Session: sess, RequestContext: context.Background()})
	var provider interface{ GatewayAuthFailureLogFields() []wklog.Field }
	if errors.As(err, &provider) && len(provider.GatewayAuthFailureLogFields()) != 0 {
		t.Fatal("plain status gained synthetic provider evidence")
	}
	if !errors.Is(err, cause) || err.Error() != cause.Error() {
		t.Fatal("plain status error was changed")
	}
}
