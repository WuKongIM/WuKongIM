package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestPresenceActivationNotReadyLoggingPreservesStatusAndRedaction(t *testing.T) {
	for _, op := range []string{presenceOpRegisterRoute, presenceOpCommitRoute, presenceOpEndpointsByUID} {
		t.Run(op, func(t *testing.T) {
			logger := newRecordingNodeLogger("internal.access.node")
			cause := fmt.Errorf("private-error-marker: %w", authoritypresence.ErrRouteNotReady)
			failure := presence.WrapActivationPhase(presence.ActivationPhaseRecovery, cause)
			authority := &activationLoggingAuthority{fakePresenceAuthority: newFakePresenceAuthority(), err: failure}
			adapter := New(Options{Authority: authority, Logger: logger})
			req := presenceRPCRequest{Op: op, Target: testPresenceTarget(), UID: "private-uid", Route: testPresenceRoute("private-uid", 101), PendingToken: "private-token"}
			body, err := encodePresenceRPCRequestBinary(req)
			if err != nil {
				t.Fatal(err)
			}
			reply, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := decodePresenceRPCResponse(reply)
			if err != nil {
				t.Fatal(err)
			}
			if response.Status != rpcStatusRouteNotReady || !errors.Is(presenceRPCErrorForStatus(response.Status), authoritypresence.ErrRouteNotReady) {
				t.Fatalf("wire error changed: %s", response.Status)
			}
			if op == presenceOpEndpointsByUID {
				if len(*logger.entries) != 0 {
					t.Fatalf("ordinary endpoint not-ready must not add activation logs: %+v", *logger.entries)
				}
				return
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_not_ready")
			allowed := map[string]bool{"event": true, "op": true, "status": true, "activation_phase": true, "hashSlot": true, "slotID": true, "leaderNodeID": true}
			allowed["activation_route_hash"] = op == presenceOpRegisterRoute
			phaseSeen := false
			for _, f := range entry.fields {
				if !allowed[f.Key] {
					t.Errorf("unexpected log field %s", f.Key)
				}
				text := fmt.Sprint(f.Value)
				for _, private := range []string{"private-uid", "private-token", "private-error-marker"} {
					if strings.Contains(text, private) {
						t.Errorf("private text leaked in %s", f.Key)
					}
				}
				if f.Key == "activation_phase" && f.Value == "recovery" {
					phaseSeen = true
				}
			}
			if !phaseSeen {
				t.Fatal("closed authority recovery phase missing")
			}
			if len(*logger.entries) != 1 {
				t.Fatalf("want one event per failed activation call, got %d", len(*logger.entries))
			}
		})
	}
}

type activationLoggingAuthority struct {
	*fakePresenceAuthority
	err error
}

func (a *activationLoggingAuthority) RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error) {
	return presence.RegisterResult{}, a.err
}
func (a *activationLoggingAuthority) CommitRoute(context.Context, presence.RouteTarget, string) error {
	return a.err
}
func (a *activationLoggingAuthority) EndpointsByUID(context.Context, presence.RouteTarget, string) ([]presence.Route, error) {
	return nil, a.err
}
