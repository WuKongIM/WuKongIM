package node

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestPresenceProviderFailureLoggingIsFixedAndLookupSilent(t *testing.T) {
	for _, op := range []string{presenceOpRegisterRoute, presenceOpCommitRoute, presenceOpEndpointsByUID} {
		t.Run(op, func(t *testing.T) {
			logger := newRecordingNodeLogger("internal.access.node")
			data := presence.RecoveryProviderFailureEvidence{Reason: presence.RecoveryProviderOwnerUnavailable, Calls: 3, FailureElapsed: 310 * time.Millisecond,
				Observed:      presence.RecoveryProviderAdmissionStage.Mask() | presence.RecoveryProviderOwnerReadStage.Mask() | presence.RecoveryProviderObservedOwnerBudget | presence.RecoveryProviderObservedOwnerDispatch | presence.RecoveryProviderObservedOwnerElapsed | presence.RecoveryProviderObservedOwnerError,
				OwnersPlanned: 3, OwnersCompleted: 1, OwnerNodeID: 2, OwnerMode: presence.RecoveryProviderOwnerRemoteCalls, OwnerError: presence.RecoveryProviderOwnerTimeout,
				PageStart: 1, OwnerConfiguredBudget: 250 * time.Millisecond, OwnerEffectiveBudget: 100 * time.Millisecond, OwnerElapsed: 300 * time.Millisecond}
			data.Stages[presence.RecoveryProviderOwnerReadStage] = 300 * time.Millisecond
			failure := presence.WrapActivationPhase(presence.ActivationPhaseRecoveryProvider, presence.WithRecoveryProviderFailure(fmt.Errorf("private-cause: %w", authority.ErrRouteNotReady), data))
			adapter := New(Options{Authority: &activationLoggingAuthority{fakePresenceAuthority: newFakePresenceAuthority(), err: failure}, Logger: logger})
			body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: op, Target: testPresenceTarget(), UID: "private-uid", Route: testPresenceRoute("private-uid", 101), PendingToken: "private-token"})
			if err != nil {
				t.Fatal(err)
			}
			reply, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := decodePresenceRPCResponse(reply)
			if err != nil || response.Status != rpcStatusRouteNotReady {
				t.Fatalf("status changed: %s/%v", response.Status, err)
			}
			if op == presenceOpEndpointsByUID {
				if len(*logger.entries) != 0 {
					t.Fatal("lookup added provider event")
				}
				return
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_not_ready")
			got := make(map[string]string)
			for _, field := range entry.fields {
				value := fmt.Sprint(field.Value)
				for _, secret := range []string{"private-cause", "private-uid", "private-token"} {
					if strings.Contains(value, secret) {
						t.Fatalf("private value in %s", field.Key)
					}
				}
				got[field.Key] = value
			}
			for key, want := range map[string]string{"recovery_provider_reason": "owner_unavailable", "recovery_provider_scope": "physical_round", "recovery_provider_calls": "3", "recovery_provider_owner_mode": "remote_calls", "recovery_provider_owner_error": "timeout", "recovery_provider_admission_ns": "0", "recovery_provider_owner_read_ns": "300000000", "recovery_provider_owner_configured_ns": "250000000", "recovery_provider_owner_effective_ns": "100000000", "recovery_provider_owner_elapsed_ns": "300000000"} {
				if got[key] != want {
					t.Errorf("%s=%q, want %q", key, got[key], want)
				}
			}
			for _, absent := range []string{"recovery_provider_snapshot_before_ns", "recovery_provider_page_next"} {
				if _, exists := got[absent]; exists {
					t.Errorf("unobserved field %s invented", absent)
				}
			}
			if len(*logger.entries) != 1 {
				t.Fatal("failure added multiple events")
			}
		})
	}
}
