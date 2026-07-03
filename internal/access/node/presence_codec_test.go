package node

import (
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
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
			name: "touch routes",
			req: presenceRPCRequest{
				Op:     presenceOpTouchRoutes,
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
	tooManyRoutes = append(tooManyRoutes, presenceOpTouchRoutesID)
	tooManyRoutes = appendPresenceRouteTarget(tooManyRoutes, testPresenceTarget())
	tooManyRoutes = appendPresenceRoute(tooManyRoutes, presence.Route{})
	tooManyRoutes = appendUvarint(tooManyRoutes, uint64(maxPresenceRPCCollectionLen+1))
	if _, err := decodePresenceRPCRequest(tooManyRoutes); err == nil {
		t.Fatal("decodePresenceRPCRequest() accepted oversized routes collection")
	}
}

func TestPresenceRPCCodecRoundTripTouchRoutes(t *testing.T) {
	req := presenceRPCRequest{
		Op:     presenceOpTouchRoutes,
		Target: presence.RouteTarget{HashSlot: 7, SlotID: 8, LeaderNodeID: 9, LeaderTerm: 12, ConfigEpoch: 13, RouteRevision: 10, AuthorityEpoch: 11},
		Routes: []presence.Route{{
			UID:           "u1",
			OwnerNodeID:   1,
			OwnerBootID:   2,
			OwnerSeq:      3,
			SessionID:     4,
			DeviceID:      "d1",
			DeviceFlag:    1,
			DeviceLevel:   0,
			Listener:      "tcp",
			ConnectedUnix: 100,
			LastSeenUnix:  150,
		}},
	}
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}
	got, err := decodePresenceRPCRequest(body)
	if err != nil {
		t.Fatalf("decodePresenceRPCRequest() error = %v", err)
	}
	if got.Op != req.Op {
		t.Fatalf("op = %q, want %q", got.Op, req.Op)
	}
	if !reflect.DeepEqual(got.Target, req.Target) {
		t.Fatalf("target = %#v, want %#v", got.Target, req.Target)
	}
	if !reflect.DeepEqual(got.Routes, req.Routes) {
		t.Fatalf("routes = %#v, want %#v", got.Routes, req.Routes)
	}
}

func TestPresenceCodecGoldenTouchRoutesWireLayout(t *testing.T) {
	req := presenceRPCRequest{
		Op:     presenceOpTouchRoutes,
		Target: presence.RouteTarget{HashSlot: 7, SlotID: 8, LeaderNodeID: 9, LeaderTerm: 12, ConfigEpoch: 13, RouteRevision: 10, AuthorityEpoch: 11},
		Routes: []presence.Route{{
			UID:           "u1",
			OwnerNodeID:   1,
			OwnerBootID:   2,
			OwnerSeq:      3,
			SessionID:     4,
			DeviceID:      "d1",
			DeviceFlag:    1,
			DeviceLevel:   0,
			Listener:      "tcp",
			ConnectedUnix: 100,
			LastSeenUnix:  150,
		}},
	}

	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}
	want := []byte{
		'W', 'K', 'V', 'P', 2,
		presenceOpTouchRoutesID,
		7, 8, 9, 12, 13, 10, 11,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		1,
		2, 'u', '1', 1, 2, 3, 4, 2, 'd', '1', 1, 0, 3, 't', 'c', 'p', 0xc8, 0x01, 0xac, 0x02,
		0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("touch route wire bytes = %#v, want %#v", body, want)
	}

	respBody, err := encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodePresenceRPCResponseBinary() error = %v", err)
	}
	wantResp := []byte{'W', 'K', 'V', 'R', 2, 2, 'o', 'k', 0, 0, 0}
	if !reflect.DeepEqual(respBody, wantResp) {
		t.Fatalf("response wire bytes = %#v, want %#v", respBody, wantResp)
	}

	endpointRespBody, err := encodePresenceRPCResponseBinary(presenceRPCResponse{
		Status: rpcStatusOK,
		Endpoints: []presence.Route{{
			UID:           "u1",
			OwnerNodeID:   1,
			OwnerBootID:   2,
			OwnerSeq:      3,
			SessionID:     4,
			DeviceID:      "d1",
			DeviceFlag:    1,
			DeviceLevel:   0,
			Listener:      "tcp",
			ConnectedUnix: 100,
			LastSeenUnix:  150,
		}},
	})
	if err != nil {
		t.Fatalf("encodePresenceRPCResponseBinary(endpoints) error = %v", err)
	}
	wantEndpointResp := []byte{
		'W', 'K', 'V', 'R', 2,
		2, 'o', 'k',
		0, 0,
		1,
		2, 'u', '1', 1, 2, 3, 4, 2, 'd', '1', 1, 0, 3, 't', 'c', 'p', 0xc8, 0x01, 0xac, 0x02,
	}
	if !reflect.DeepEqual(endpointRespBody, wantEndpointResp) {
		t.Fatalf("endpoint response wire bytes = %#v, want %#v", endpointRespBody, wantEndpointResp)
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

	wantReq := []byte{'W', 'K', 'V', 'P', 2}
	wantResp := []byte{'W', 'K', 'V', 'R', 2}
	if string(reqBody[:len(wantReq)]) != string(wantReq) {
		t.Fatalf("request magic = %v, want %v", reqBody[:len(wantReq)], wantReq)
	}
	if string(respBody[:len(wantResp)]) != string(wantResp) {
		t.Fatalf("response magic = %v, want %v", respBody[:len(wantResp)], wantResp)
	}

	v1Req := append([]byte(nil), reqBody...)
	v1Req[4] = 1
	if _, err := decodePresenceRPCRequest(v1Req); err == nil {
		t.Fatal("decodePresenceRPCRequest() accepted v1 request magic")
	}
	v1Resp := append([]byte(nil), respBody...)
	v1Resp[4] = 1
	if _, err := decodePresenceRPCResponse(v1Resp); err == nil {
		t.Fatal("decodePresenceRPCResponse() accepted v1 response magic")
	}
}
