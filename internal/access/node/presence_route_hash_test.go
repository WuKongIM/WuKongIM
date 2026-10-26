package node

import (
	"context"
	"fmt"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestPresenceRegisterOriginalWireRouteMatchesGatewayHashAndCommitIsUnknown(t *testing.T) {
	for _, op := range []string{presenceOpRegisterRoute, presenceOpCommitRoute} {
		t.Run(op, func(t *testing.T) {
			logger := newRecordingNodeLogger("internal.access.node")
			adapter := New(Options{Authority: &activationLoggingAuthority{fakePresenceAuthority: newFakePresenceAuthority(), err: authority.ErrRouteNotReady}, Logger: logger})
			route := testPresenceRoute("private-uid", 11)
			route.OwnerNodeID, route.OwnerBootID = 9, 99
			if op == presenceOpCommitRoute {
				route = presence.Route{} // Client.CommitRoute populates only the token.
			}
			body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: op, Target: testPresenceTarget(), Route: route, PendingToken: "private-token"})
			if err != nil {
				t.Fatal(err)
			}
			reply, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := decodePresenceRPCResponse(reply)
			if err != nil || resp.Status != rpcStatusRouteNotReady {
				t.Fatalf("wire status=%v/%v", resp.Status, err)
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_not_ready")
			got := ""
			for _, f := range entry.fields {
				if f.Key == "activation_route_hash" {
					got = fmt.Sprint(f.Value)
				}
			}
			want := "7718ef07734da84ac7c13fc5b43b11de"
			if op == presenceOpCommitRoute {
				want = ""
			}
			if got != want {
				t.Fatalf("decoded route hash=%q want%q", got, want)
			}
			if op == presenceOpCommitRoute {
				decoded, err := decodePresenceRPCRequest(body)
				if err != nil || decoded.Route != (presence.Route{}) {
					t.Fatal("Commit unexpectedly gained a route wire field")
				}
			}
		})
	}
}
