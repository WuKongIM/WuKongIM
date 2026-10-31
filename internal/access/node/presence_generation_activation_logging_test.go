package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// The complete-generation wire path must retain the same local failure
// evidence as legacy activation, before the status drops its source phase.
func TestGenerationActivationFailureRetainsExistingBoundedLog(t *testing.T) {
	for _, op := range []byte{generationRegister, generationCommit, generationAbortRoute, generationTouch, generationUnregister} {
		t.Run(fmt.Sprint(op), func(t *testing.T) {
			claim, route := wireGeneration(), wireRoute()
			logger := newRecordingNodeLogger("internal.access.node")
			data := presence.RecoveryProviderFailureEvidence{
				Reason: presence.RecoveryProviderOwnerUnavailable, Calls: 3,
				Observed: presence.RecoveryProviderAdmissionStage.Mask() | presence.RecoveryProviderOwnerReadStage.Mask() |
					presence.RecoveryProviderObservedOwnerDispatch | presence.RecoveryProviderObservedOwnerError,
				OwnersPlanned: 3, OwnersCompleted: 1, OwnerNodeID: 2,
				OwnerMode: presence.RecoveryProviderOwnerRemoteCalls, OwnerError: presence.RecoveryProviderOwnerTimeout,
			}
			data.Stages[presence.RecoveryProviderOwnerReadStage] = 300 * time.Millisecond
			failure := presence.WrapActivationPhase(presence.ActivationPhaseRecoveryProvider,
				presence.WithRecoveryProviderFailure(fmt.Errorf("private-cause: %w", authority.ErrRouteNotReady), data))
			if _, ok := presence.RecoveryProviderFailure(failure); !ok {
				t.Fatal("fixture omitted required provider observation mask")
			}
			port := &generationLoggingAuthority{generationAuthorityFixture: generationAuthorityFixture{claim: claim}, failure: failure}
			adapter := New(Options{GenerationAuthority: port, Logger: logger})
			request := generationRequest{Op: op, Claim: claim, Route: route, Routes: []presence.Route{route}, Token: "private-token", Identity: route.Identity(), OwnerSeq: route.OwnerSeq}
			body, err := encodeGenerationRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			reply, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := encodeGenerationResponse(generationResponse{Op: op, Status: rpcStatusRouteNotReady})
			if err != nil || !bytes.Equal(reply, baseline) {
				t.Fatalf("diagnostics changed complete-generation response bytes: %v", err)
			}
			response, err := decodeGenerationResponse(reply, op)
			if err != nil || !errors.Is(generationError(response.Status), authority.ErrRouteNotReady) {
				t.Fatalf("wire cause changed: %v / %v", response.Status, err)
			}
			if op != generationRegister && op != generationCommit {
				if len(*logger.entries) != 0 {
					t.Fatal("non-activation operation added an activation failure event")
				}
				return
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_not_ready")
			fields := make(map[string]string)
			for _, field := range entry.fields {
				for _, forbidden := range []string{"uid", "deviceID", "device_id", "sessionID", "session_id", "token", "error", "cause"} {
					if field.Key == forbidden {
						t.Fatalf("private field %s added", field.Key)
					}
				}
				value := fmt.Sprint(field.Value)
				for _, private := range []string{"private-cause", "private-token", route.UID, route.DeviceID} {
					// Fixture device IDs can be one character; compare only full
					// retained private values that cannot match closed vocabulary.
					if len(private) > 3 && strings.Contains(value, private) {
						t.Fatalf("private data entered %s", field.Key)
					}
				}
				fields[field.Key] = value
			}
			for key, want := range map[string]string{
				"activation_phase": "recovery_provider", "status": rpcStatusRouteNotReady,
				"hashSlot": fmt.Sprint(claim.Target.HashSlot), "slotID": fmt.Sprint(claim.Target.SlotID),
				"leaderNodeID":             fmt.Sprint(claim.Target.LeaderNodeID),
				"recovery_provider_reason": "owner_unavailable", "recovery_provider_scope": "physical_round",
				"recovery_provider_calls": "3", "recovery_provider_owner_read_ns": "300000000",
			} {
				if fields[key] != want {
					t.Errorf("%s=%q want %q", key, fields[key], want)
				}
			}
			if _, exists := fields["recovery_provider_snapshot_before_ns"]; exists {
				t.Fatal("missing provider timing was invented")
			}
			if op == generationRegister {
				want := fmt.Sprintf("%x", presence.ActivationRouteHash(route.OwnerNodeID, route.OwnerBootID, route.SessionID))
				if fields["op"] != presenceOpRegisterRoute || fields["activation_route_hash"] != want {
					t.Fatal("original decoded register route lost gateway correlation")
				}
			} else if fields["op"] != presenceOpCommitRoute || fields["activation_route_hash"] != "" {
				t.Fatal("token-only commit invented an original route identity")
			}
			if len(*logger.entries) != 1 {
				t.Fatal("one failed activation produced multiple events")
			}
		})
	}
}

func TestGenerationActivationFailureThroughClientDoesNotRetryOrDowngrade(t *testing.T) {
	claim, route := wireGeneration(), wireRoute()
	logger := newRecordingNodeLogger("internal.access.node")
	port := &generationLoggingAuthority{generationAuthorityFixture: generationAuthorityFixture{claim: claim}, failure: authority.ErrRouteNotReady}
	network := &generationLoopback{adapter: New(Options{GenerationAuthority: port, Logger: logger})}
	_, err := NewClient(network).RegisterPresenceGeneration(context.Background(), claim.Target, route)
	if !errors.Is(err, authority.ErrRouteNotReady) || network.calls != 1 {
		t.Fatalf("failure replayed or changed: %v / %d calls", err, network.calls)
	}
	entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_not_ready")
	for _, field := range entry.fields {
		if field.Key == "activation_phase" && field.Value != "unknown" {
			t.Fatal("plain status acquired an invented source phase")
		}
		if strings.HasPrefix(field.Key, "recovery_provider_") {
			t.Fatal("absent provider evidence was manufactured")
		}
	}
}

func TestGenerationActivationSuccessAndAbsentLoggerPreserveOutcomes(t *testing.T) {
	claim, route, cut := wireGeneration(), wireRoute(), wireCut()
	logger := newRecordingNodeLogger("internal.access.node")
	port := &generationLoggingAuthority{generationAuthorityFixture: generationAuthorityFixture{claim: claim,
		receipt: presence.ActivationReceipt{Claim: claim, Owner: cut, Identity: route.Identity(), OwnerSeq: route.OwnerSeq}}}
	network := &generationLoopback{adapter: New(Options{GenerationAuthority: port, Logger: logger})}
	client := NewClient(network)
	if _, err := client.RegisterPresenceGeneration(context.Background(), claim.Target, route); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CommitPresenceGeneration(context.Background(), claim, "private-token"); err != nil {
		t.Fatal(err)
	}
	if len(*logger.entries) != 0 || network.calls != 2 {
		t.Fatal("success emitted failure diagnostics or changed calls")
	}
	port.failure = authority.ErrRouteNotReady
	network.adapter = New(Options{GenerationAuthority: port})
	if _, err := client.RegisterPresenceGeneration(context.Background(), claim.Target, route); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("absent logger changed original failure: %v", err)
	}
}

func TestGenerationUnknownActivationFailureDoesNotAddRawCauseEvent(t *testing.T) {
	logger := newRecordingNodeLogger("internal.access.node")
	port := &generationLoggingAuthority{failure: errors.New("private-raw-cause")}
	network := &generationLoopback{adapter: New(Options{GenerationAuthority: port, Logger: logger})}
	if _, err := NewClient(network).RegisterPresenceGeneration(context.Background(), wireGeneration().Target, wireRoute()); err == nil {
		t.Fatal("original unknown failure became success")
	}
	if len(*logger.entries) != 0 || network.calls != 1 {
		t.Fatal("unknown source error added a raw-cause log or retry")
	}
}

type generationLoggingAuthority struct {
	generationAuthorityFixture
	failure error
}

func (a *generationLoggingAuthority) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	if a.failure != nil {
		return presence.RegisterResult{}, a.failure
	}
	return a.generationAuthorityFixture.RegisterRoute(ctx, target, route)
}
func (a *generationLoggingAuthority) CommitRouteWithReceipt(ctx context.Context, claim presence.GenerationClaim, token presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	if a.failure != nil {
		return presence.ActivationReceipt{}, a.failure
	}
	return a.generationAuthorityFixture.CommitRouteWithReceipt(ctx, claim, token)
}
func (a *generationLoggingAuthority) AbortRouteWithClaim(context.Context, presence.GenerationClaim, presence.PendingRouteToken) error {
	return a.failure
}
func (a *generationLoggingAuthority) TouchRoutes(context.Context, presence.GenerationClaim, []presence.Route) error {
	return a.failure
}
func (a *generationLoggingAuthority) UnregisterRoute(context.Context, presence.GenerationClaim, presence.RouteIdentity, uint64) error {
	return a.failure
}
