package node

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
)

func TestPresenceCodecRequestRoundTrip(t *testing.T) {
	target := testPresenceTarget()
	route := testPresenceRoute("u1", 101)

	tests := []struct {
		name string
		req  presenceRPCRequest
	}{
		{
			name: "register",
			req: presenceRPCRequest{
				Op:     presenceOpRegisterRoute,
				Target: target,
				Route:  route,
			},
		},
		{
			name: "commit",
			req: presenceRPCRequest{
				Op:           presenceOpCommitRoute,
				Target:       target,
				PendingToken: "pending-1",
			},
		},
		{
			name: "abort",
			req: presenceRPCRequest{
				Op:           presenceOpAbortRoute,
				Target:       target,
				PendingToken: "pending-2",
			},
		},
		{
			name: "unregister",
			req: presenceRPCRequest{
				Op:       presenceOpUnregisterRoute,
				Target:   target,
				Identity: route.Identity(),
				OwnerSeq: 77,
			},
		},
		{
			name: "endpoints",
			req: presenceRPCRequest{
				Op:     presenceOpEndpointsByUID,
				Target: target,
				UID:    "u1",
			},
		},
		{
			name: "rehydrate",
			req: presenceRPCRequest{
				Op:     presenceOpRehydrateRoutes,
				Target: target,
				Routes: []presence.Route{
					route,
					testPresenceRoute("u2", 202),
				},
			},
		},
		{
			name: "owner action",
			req: presenceRPCRequest{
				Op: presenceOpApplyRouteAction,
				Action: presence.RouteAction{
					UID:         "u1",
					OwnerNodeID: 13,
					OwnerBootID: 23,
					SessionID:   101,
					Kind:        "close",
					Reason:      "presence_conflict",
					DelayMS:     7,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := encodePresenceRPCRequestBinary(tt.req)
			if err != nil {
				t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
			}
			if !isPresenceRPCRequestBinary(body) {
				t.Fatalf("request missing binary magic: %q", body)
			}
			again, err := encodePresenceRPCRequestBinary(tt.req)
			if err != nil {
				t.Fatalf("second encodePresenceRPCRequestBinary() error = %v", err)
			}
			if string(body) != string(again) {
				t.Fatalf("encodePresenceRPCRequestBinary() is not deterministic")
			}

			got, err := decodePresenceRPCRequest(body)
			if err != nil {
				t.Fatalf("decodePresenceRPCRequest() error = %v", err)
			}
			if diffPresenceRPCRequest(got, tt.req) != "" {
				t.Fatalf("decodePresenceRPCRequest() mismatch: %s", diffPresenceRPCRequest(got, tt.req))
			}
		})
	}
}

func TestPresenceCodecResponseRoundTrip(t *testing.T) {
	route := testPresenceRoute("u1", 101)
	responses := []presenceRPCResponse{
		{
			Status: rpcStatusOK,
			Register: presence.RegisterResult{
				PendingToken: "pending-1",
				Actions: []presence.RouteAction{{
					UID:         "u1",
					OwnerNodeID: 1,
					OwnerBootID: 2,
					SessionID:   3,
					Kind:        "close",
					Reason:      "conflict",
					DelayMS:     -5,
				}},
			},
		},
		{
			Status:    rpcStatusOK,
			Endpoints: []presence.Route{route, testPresenceRoute("u2", 202)},
		},
		{
			Status: rpcStatusOK,
			Rehydrate: []presence.RehydrateResult{{
				Route:        route.Identity(),
				Accepted:     true,
				PendingToken: "rehydrate-pending-1",
				Actions:      []presence.RouteAction{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, SessionID: 3, Kind: "close", Reason: "replace"}},
			}, {
				Route: testPresenceRoute("u2", 202).Identity(),
				Error: "stale",
			}},
		},
		{Status: rpcStatusNotLeader},
		{Status: rpcStatusStaleRoute},
		{Status: rpcStatusRouteNotReady},
		{Status: rpcStatusRejected},
	}

	for _, resp := range responses {
		body, err := encodePresenceRPCResponseBinary(resp)
		if err != nil {
			t.Fatalf("encodePresenceRPCResponseBinary(%q) error = %v", resp.Status, err)
		}
		if !isPresenceRPCResponseBinary(body) {
			t.Fatalf("response missing binary magic: %q", body)
		}
		again, err := encodePresenceRPCResponseBinary(resp)
		if err != nil {
			t.Fatalf("second encodePresenceRPCResponseBinary(%q) error = %v", resp.Status, err)
		}
		if string(body) != string(again) {
			t.Fatalf("encodePresenceRPCResponseBinary(%q) is not deterministic", resp.Status)
		}

		got, err := decodePresenceRPCResponse(body)
		if err != nil {
			t.Fatalf("decodePresenceRPCResponse(%q) error = %v", resp.Status, err)
		}
		if diffPresenceRPCResponse(got, resp) != "" {
			t.Fatalf("decodePresenceRPCResponse(%q) mismatch: %s", resp.Status, diffPresenceRPCResponse(got, resp))
		}
	}
}

func TestPresenceCodecRejectsMalformedTruncatedAndTrailingBytes(t *testing.T) {
	reqBody, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
		Op:     presenceOpEndpointsByUID,
		Target: testPresenceTarget(),
		UID:    "u1",
	})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}
	respBody, err := encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodePresenceRPCResponseBinary() error = %v", err)
	}

	badReqMagic := append([]byte(nil), reqBody...)
	badReqMagic[0] = 'X'
	if _, err := decodePresenceRPCRequest(badReqMagic); err == nil {
		t.Fatalf("decodePresenceRPCRequest() accepted bad magic")
	}
	if _, err := decodePresenceRPCRequest(reqBody[:len(reqBody)-1]); err == nil {
		t.Fatalf("decodePresenceRPCRequest() accepted truncated body")
	}
	if _, err := decodePresenceRPCRequest(append(append([]byte(nil), reqBody...), 0)); err == nil {
		t.Fatalf("decodePresenceRPCRequest() accepted trailing bytes")
	}

	badRespMagic := append([]byte(nil), respBody...)
	badRespMagic[0] = 'X'
	if _, err := decodePresenceRPCResponse(badRespMagic); err == nil {
		t.Fatalf("decodePresenceRPCResponse() accepted bad magic")
	}
	if _, err := decodePresenceRPCResponse(respBody[:len(respBody)-1]); err == nil {
		t.Fatalf("decodePresenceRPCResponse() accepted truncated body")
	}
	if _, err := decodePresenceRPCResponse(append(append([]byte(nil), respBody...), 0)); err == nil {
		t.Fatalf("decodePresenceRPCResponse() accepted trailing bytes")
	}
}

func TestPresenceCodecRejectsUnknownOpAndCollectionOverflow(t *testing.T) {
	unknownOp := append([]byte(nil), presenceRPCRequestMagic[:]...)
	unknownOp = append(unknownOp, 99)
	if _, err := decodePresenceRPCRequest(unknownOp); err == nil {
		t.Fatal("decodePresenceRPCRequest() accepted unknown op id")
	}

	tooManyRoutes := append([]byte(nil), presenceRPCRequestMagic[:]...)
	tooManyRoutes = append(tooManyRoutes, presenceOpRehydrateRoutesID)
	tooManyRoutes = appendPresenceRouteTarget(tooManyRoutes, testPresenceTarget())
	tooManyRoutes = appendPresenceRoute(tooManyRoutes, presence.Route{})
	tooManyRoutes = appendUvarint(tooManyRoutes, uint64(maxPresenceRPCCollectionLen+1))
	if _, err := decodePresenceRPCRequest(tooManyRoutes); err == nil {
		t.Fatal("decodePresenceRPCRequest() accepted oversized routes collection")
	}
}

func TestPresenceCodecUsesTaskMagicHeaders(t *testing.T) {
	reqBody, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpCommitRoute, Target: testPresenceTarget(), PendingToken: "t1"})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}
	respBody, err := encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodePresenceRPCResponseBinary() error = %v", err)
	}

	wantReq := []byte{'W', 'K', 'V', 'P', 1}
	wantResp := []byte{'W', 'K', 'V', 'R', 1}
	if string(reqBody[:len(wantReq)]) != string(wantReq) {
		t.Fatalf("request magic = %v, want %v", reqBody[:len(wantReq)], wantReq)
	}
	if string(respBody[:len(wantResp)]) != string(wantResp) {
		t.Fatalf("response magic = %v, want %v", respBody[:len(wantResp)], wantResp)
	}
}
