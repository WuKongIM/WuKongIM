package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestPresenceAuthorityRPCHandlerDispatchesOperations(t *testing.T) {
	target := testPresenceTarget()
	route := testPresenceRoute("u1", 101)

	tests := []struct {
		name   string
		req    presenceRPCRequest
		assert func(*testing.T, *fakePresenceAuthority, presenceRPCResponse)
	}{
		{
			name: "register",
			req: presenceRPCRequest{
				Op:     presenceOpRegisterRoute,
				Target: target,
				Route:  route,
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.registerCalls) != 1 {
					t.Fatalf("register calls = %d, want 1", len(authority.registerCalls))
				}
				if !reflect.DeepEqual(authority.registerCalls[0].target, target) || !reflect.DeepEqual(authority.registerCalls[0].route, route) {
					t.Fatalf("register call = %#v", authority.registerCalls[0])
				}
				if resp.Register.PendingToken != "pending-1" {
					t.Fatalf("register pending token = %q, want pending-1", resp.Register.PendingToken)
				}
			},
		},
		{
			name: "commit",
			req: presenceRPCRequest{
				Op:           presenceOpCommitRoute,
				Target:       target,
				PendingToken: "pending-1",
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.commitCalls) != 1 {
					t.Fatalf("commit calls = %d, want 1", len(authority.commitCalls))
				}
				if authority.commitCalls[0].token != "pending-1" {
					t.Fatalf("commit token = %q, want pending-1", authority.commitCalls[0].token)
				}
			},
		},
		{
			name: "abort",
			req: presenceRPCRequest{
				Op:           presenceOpAbortRoute,
				Target:       target,
				PendingToken: "pending-1",
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.abortCalls) != 1 {
					t.Fatalf("abort calls = %d, want 1", len(authority.abortCalls))
				}
				if authority.abortCalls[0].token != "pending-1" {
					t.Fatalf("abort token = %q, want pending-1", authority.abortCalls[0].token)
				}
			},
		},
		{
			name: "unregister",
			req: presenceRPCRequest{
				Op:       presenceOpUnregisterRoute,
				Target:   target,
				Identity: route.Identity(),
				OwnerSeq: 88,
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.unregisterCalls) != 1 {
					t.Fatalf("unregister calls = %d, want 1", len(authority.unregisterCalls))
				}
				if authority.unregisterCalls[0].ownerSeq != 88 {
					t.Fatalf("unregister owner seq = %d, want 88", authority.unregisterCalls[0].ownerSeq)
				}
			},
		},
		{
			name: "endpoints",
			req: presenceRPCRequest{
				Op:     presenceOpEndpointsByUID,
				Target: target,
				UID:    "u1",
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.endpointCalls) != 1 {
					t.Fatalf("endpoint calls = %d, want 1", len(authority.endpointCalls))
				}
				if authority.endpointCalls[0].uid != "u1" {
					t.Fatalf("endpoint uid = %q, want u1", authority.endpointCalls[0].uid)
				}
				if len(resp.Endpoints) != 1 || resp.Endpoints[0].SessionID != 101 {
					t.Fatalf("endpoints = %#v", resp.Endpoints)
				}
			},
		},
		{
			name: "touch routes",
			req: presenceRPCRequest{
				Op:     presenceOpTouchRoutes,
				Target: target,
				Routes: []presence.Route{route},
			},
			assert: func(t *testing.T, authority *fakePresenceAuthority, resp presenceRPCResponse) {
				t.Helper()
				if len(authority.touchCalls) != 1 {
					t.Fatalf("touch calls = %d, want 1", len(authority.touchCalls))
				}
				if !reflect.DeepEqual(authority.touchCalls[0].target, target) || !reflect.DeepEqual(authority.touchCalls[0].routes, []presence.Route{route}) {
					t.Fatalf("touch call = %#v", authority.touchCalls[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authority := newFakePresenceAuthority()
			adapter := New(Options{Authority: authority})
			body, err := encodePresenceRPCRequestBinary(tt.req)
			if err != nil {
				t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
			}

			respBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatalf("HandlePresenceAuthorityRPC() error = %v", err)
			}
			resp, err := decodePresenceRPCResponse(respBody)
			if err != nil {
				t.Fatalf("decodePresenceRPCResponse() error = %v", err)
			}
			if resp.Status != rpcStatusOK {
				t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
			}
			tt.assert(t, authority, resp)
		})
	}
}

func TestPresenceAuthorityRPCHandlerMapsErrorsToStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not leader", err: authoritypresence.ErrNotLeader, want: rpcStatusNotLeader},
		{name: "stale route", err: authoritypresence.ErrStaleRoute, want: rpcStatusStaleRoute},
		{name: "route not ready", err: authoritypresence.ErrRouteNotReady, want: rpcStatusRouteNotReady},
		{name: "rejected", err: errors.New("boom"), want: rpcStatusRejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authority := newFakePresenceAuthority()
			authority.registerErr = tt.err
			adapter := New(Options{Authority: authority})
			body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
				Op:     presenceOpRegisterRoute,
				Target: testPresenceTarget(),
				Route:  testPresenceRoute("u1", 101),
			})
			if err != nil {
				t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
			}

			respBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
			if err != nil {
				t.Fatalf("HandlePresenceAuthorityRPC() error = %v", err)
			}
			resp, err := decodePresenceRPCResponse(respBody)
			if err != nil {
				t.Fatalf("decodePresenceRPCResponse() error = %v", err)
			}
			if resp.Status != tt.want {
				t.Fatalf("status = %q, want %q", resp.Status, tt.want)
			}
		})
	}
}

func TestPresenceAuthorityRPCHandlerReturnsAlignedEndpointTargetResults(t *testing.T) {
	first := testPresenceTarget()
	second := first
	second.HashSlot++
	second.SlotID++
	authority := &fakeBatchPresenceAuthority{
		fakePresenceAuthority: newFakePresenceAuthority(),
		errorsByHashSlot:      map[uint16]error{second.HashSlot: authoritypresence.ErrNotLeader},
	}
	adapter := New(Options{Authority: authority})
	groups := []presence.EndpointLookupGroup{
		{Target: first, UIDs: []string{"u1", "u2"}},
		{Target: second, UIDs: []string{"u3"}},
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpEndpointsByTargets, EndpointGroups: groups})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}

	responseBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePresenceAuthorityRPC() error = %v", err)
	}
	results, err := decodePresenceEndpointsByTargetsResponseBinary(responseBody)
	if err != nil {
		t.Fatalf("decodePresenceEndpointsByTargetsResponseBinary() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two aligned results", results)
	}
	if results[0].Status != rpcStatusOK || len(results[0].Routes) != 2 {
		t.Fatalf("results[0] = %#v, want two routes", results[0])
	}
	if results[1].Status != rpcStatusNotLeader || len(results[1].Routes) != 0 {
		t.Fatalf("results[1] = %#v, want not_leader", results[1])
	}
	if !reflect.DeepEqual(authority.batchCalls, groups) {
		t.Fatalf("batch calls = %#v, want %#v", authority.batchCalls, groups)
	}
}

func TestPresenceAuthorityRPCHandlerFallsBackToSingleUIDAuthority(t *testing.T) {
	authority := newFakePresenceAuthority()
	adapter := New(Options{Authority: authority})
	group := presence.EndpointLookupGroup{Target: testPresenceTarget(), UIDs: []string{"u1", "u2"}}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
		Op:             presenceOpEndpointsByTargets,
		EndpointGroups: []presence.EndpointLookupGroup{group},
	})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}

	responseBody, err := adapter.HandlePresenceAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePresenceAuthorityRPC() error = %v", err)
	}
	results, err := decodePresenceEndpointsByTargetsResponseBinary(responseBody)
	if err != nil {
		t.Fatalf("decodePresenceEndpointsByTargetsResponseBinary() error = %v", err)
	}
	if len(results) != 1 || results[0].Status != rpcStatusOK || len(results[0].Routes) != 2 {
		t.Fatalf("results = %#v, want one successful two-route group", results)
	}
	if len(authority.endpointCalls) != 2 || authority.endpointCalls[0].uid != "u1" || authority.endpointCalls[1].uid != "u2" {
		t.Fatalf("endpoint fallback calls = %#v", authority.endpointCalls)
	}
}

func TestPresenceOwnerRPCHandlerDispatchesAction(t *testing.T) {
	action := presence.RouteAction{
		UID:         "u1",
		OwnerNodeID: 13,
		OwnerBootID: 23,
		SessionID:   101,
		Kind:        "close",
		Reason:      "presence_conflict",
	}
	owner := &fakePresenceOwner{}
	adapter := New(Options{Owner: owner})
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpApplyRouteAction, Action: action})
	if err != nil {
		t.Fatalf("encodePresenceRPCRequestBinary() error = %v", err)
	}

	respBody, err := adapter.HandlePresenceOwnerRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePresenceOwnerRPC() error = %v", err)
	}
	resp, err := decodePresenceRPCResponse(respBody)
	if err != nil {
		t.Fatalf("decodePresenceRPCResponse() error = %v", err)
	}
	if resp.Status != rpcStatusOK {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if len(owner.actions) != 1 || !reflect.DeepEqual(owner.actions[0], action) {
		t.Fatalf("owner actions = %#v, want %#v", owner.actions, action)
	}
}

func TestPresenceClientEncodesRPCAndMapsStatusErrors(t *testing.T) {
	target := testPresenceTarget()
	node := &fakePresenceRPCNode{
		response: presenceRPCResponse{Status: rpcStatusRouteNotReady},
	}
	client := NewClient(node)

	err := client.CommitRoute(context.Background(), target, "pending-1")
	if !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("CommitRoute() error = %v, want %v", err, authoritypresence.ErrRouteNotReady)
	}
	if node.nodeID != target.LeaderNodeID {
		t.Fatalf("rpc node id = %d, want %d", node.nodeID, target.LeaderNodeID)
	}
	if node.serviceID != PresenceAuthorityRPCServiceID {
		t.Fatalf("rpc service id = %d, want %d", node.serviceID, PresenceAuthorityRPCServiceID)
	}
	req, err := decodePresenceRPCRequest(node.payload)
	if err != nil {
		t.Fatalf("decodePresenceRPCRequest(client payload) error = %v", err)
	}
	if req.Op != presenceOpCommitRoute || req.PendingToken != "pending-1" {
		t.Fatalf("client request = %#v", req)
	}
}

func TestPresenceClientEndpointsByTargetsPreservesAlignedPartialFailures(t *testing.T) {
	target := testPresenceTarget()
	groups := []presence.EndpointLookupGroup{
		{Target: target, UIDs: []string{"u1", "u2"}},
		{Target: target, UIDs: []string{"u3"}},
	}
	node := &fakePresenceBatchRPCNode{results: []presenceRPCEndpointLookupResult{
		{Status: rpcStatusOK, Routes: []presence.Route{testPresenceRoute("u1", 101)}},
		{Status: rpcStatusNotLeader},
	}}
	client := NewClient(node)

	results, err := client.EndpointsByTargets(context.Background(), target.LeaderNodeID, groups)
	if err != nil {
		t.Fatalf("EndpointsByTargets() error = %v", err)
	}
	if len(results) != 2 || len(results[0].Routes) != 1 || results[0].Err != nil {
		t.Fatalf("results[0] = %#v", results[0])
	}
	if !errors.Is(results[1].Err, authoritypresence.ErrNotLeader) {
		t.Fatalf("results[1].Err = %v, want ErrNotLeader", results[1].Err)
	}
	if node.nodeID != target.LeaderNodeID || node.serviceID != PresenceAuthorityRPCServiceID {
		t.Fatalf("rpc destination = (%d,%d)", node.nodeID, node.serviceID)
	}
	req, err := decodePresenceRPCRequest(node.payload)
	if err != nil {
		t.Fatalf("decodePresenceRPCRequest() error = %v", err)
	}
	if req.Op != presenceOpEndpointsByTargets || !reflect.DeepEqual(req.EndpointGroups, groups) {
		t.Fatalf("request = %#v, want groups %#v", req, groups)
	}
}

func TestPresenceClientEndpointsByTargetsRejectsMismatchedDestination(t *testing.T) {
	target := testPresenceTarget()
	node := &fakePresenceBatchRPCNode{}
	client := NewClient(node)

	_, err := client.EndpointsByTargets(context.Background(), target.LeaderNodeID+1, []presence.EndpointLookupGroup{{
		Target: target,
		UIDs:   []string{"u1"},
	}})
	if err == nil {
		t.Fatal("EndpointsByTargets() error = nil, want target leader mismatch")
	}
	if len(node.payload) != 0 {
		t.Fatal("EndpointsByTargets() called transport after destination mismatch")
	}
}

func TestPresenceClientEndpointsByTargetsRejectsMisalignedResponse(t *testing.T) {
	target := testPresenceTarget()
	client := NewClient(&fakePresenceBatchRPCNode{results: []presenceRPCEndpointLookupResult{{Status: rpcStatusOK}}})

	_, err := client.EndpointsByTargets(context.Background(), target.LeaderNodeID, []presence.EndpointLookupGroup{
		{Target: target, UIDs: []string{"u1"}},
		{Target: target, UIDs: []string{"u2"}},
	})
	if err == nil {
		t.Fatal("EndpointsByTargets() error = nil, want result cardinality error")
	}
}

func TestPresenceClientEndpointsByTargetsFallsBackWhenBatchServiceIsUnavailable(t *testing.T) {
	target := testPresenceTarget()
	authority := newFakePresenceAuthority()
	node := &rollingUpgradePresenceRPCNode{adapter: New(Options{Authority: authority})}
	client := NewClient(node)
	groups := []presence.EndpointLookupGroup{
		{Target: target, UIDs: []string{"u1", "u2"}},
		{Target: target, UIDs: []string{"u3"}},
	}

	results, err := client.EndpointsByTargets(context.Background(), target.LeaderNodeID, groups)
	if err != nil {
		t.Fatalf("EndpointsByTargets() error = %v", err)
	}
	if len(results) != 2 || len(results[0].Routes) != 2 || len(results[1].Routes) != 1 {
		t.Fatalf("fallback results = %#v", results)
	}
	if node.batchServiceCalls != 1 || node.legacyServiceCalls != 3 {
		t.Fatalf("service calls batch/legacy = %d/%d, want 1/3", node.batchServiceCalls, node.legacyServiceCalls)
	}
	if len(authority.endpointCalls) != 3 {
		t.Fatalf("legacy endpoint calls = %#v, want 3", authority.endpointCalls)
	}
}

func TestPresenceClientEndpointsByTargetsDoesNotFallbackForSimilarRemoteErrors(t *testing.T) {
	target := testPresenceTarget()
	groups := []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"u1"}}}
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "different remote code",
			err: transport.RemoteError{
				Code:    "permission_denied",
				Message: "internal/access/node: unknown presence op id 8",
			},
		},
		{
			name: "similar remote message",
			err: transport.RemoteError{
				Code:    "remote_error",
				Message: "proxy rejected: unknown presence op id 8",
			},
		},
		{
			name: "different unknown operation",
			err: transport.RemoteError{
				Code:    "remote_error",
				Message: "internal/access/node: unknown presence op id 7",
			},
		},
		{
			name: "non remote error",
			err:  errors.New("internal/access/node: unknown presence op id 8"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &rejectingPresenceRPCNode{err: tt.err}
			client := NewClient(node)

			results, err := client.EndpointsByTargets(context.Background(), target.LeaderNodeID, groups)
			if err == nil {
				t.Fatalf("EndpointsByTargets() error = nil, want %v", tt.err)
			}
			if err.Error() != tt.err.Error() {
				t.Fatalf("EndpointsByTargets() error = %q, want %q", err, tt.err)
			}
			if results != nil {
				t.Fatalf("EndpointsByTargets() results = %#v, want nil", results)
			}
			if node.calls != 1 {
				t.Fatalf("CallRPC() calls = %d, want one batch attempt without legacy fallback", node.calls)
			}
		})
	}
}

func TestClientTouchRoutesCallsPresenceAuthorityService(t *testing.T) {
	target := testPresenceTarget()
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}
	node := &fakePresenceRPCNode{
		response: presenceRPCResponse{Status: rpcStatusOK},
	}
	client := NewClient(node)

	if err := client.TouchRoutes(context.Background(), target, routes); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}
	if node.nodeID != target.LeaderNodeID {
		t.Fatalf("rpc node id = %d, want %d", node.nodeID, target.LeaderNodeID)
	}
	if node.serviceID != PresenceAuthorityRPCServiceID {
		t.Fatalf("rpc service id = %d, want %d", node.serviceID, PresenceAuthorityRPCServiceID)
	}
	req, err := decodePresenceRPCRequest(node.payload)
	if err != nil {
		t.Fatalf("decodePresenceRPCRequest(client payload) error = %v", err)
	}
	if req.Op != presenceOpTouchRoutes {
		t.Fatalf("op = %q, want %q", req.Op, presenceOpTouchRoutes)
	}
	if !reflect.DeepEqual(req.Target, target) {
		t.Fatalf("target = %#v, want %#v", req.Target, target)
	}
	if !reflect.DeepEqual(req.Routes, routes) {
		t.Fatalf("routes = %#v, want %#v", req.Routes, routes)
	}
}

func TestPresenceClientEncodesOwnerActionRPC(t *testing.T) {
	node := &fakePresenceRPCNode{
		response: presenceRPCResponse{Status: rpcStatusOK},
	}
	client := NewClient(node)
	action := presence.RouteAction{UID: "u1", OwnerNodeID: 2, OwnerBootID: 23, SessionID: 101, Kind: "close", Reason: "presence_conflict"}

	if err := client.ApplyRouteAction(context.Background(), 2, action); err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if node.nodeID != 2 {
		t.Fatalf("rpc node id = %d, want 2", node.nodeID)
	}
	if node.serviceID != PresenceOwnerRPCServiceID {
		t.Fatalf("rpc service id = %d, want %d", node.serviceID, PresenceOwnerRPCServiceID)
	}
	req, err := decodePresenceRPCRequest(node.payload)
	if err != nil {
		t.Fatalf("decodePresenceRPCRequest(client payload) error = %v", err)
	}
	if req.Op != presenceOpApplyRouteAction || !reflect.DeepEqual(req.Action, action) {
		t.Fatalf("client request = %#v", req)
	}
}

func TestPresenceClientRejectsUnknownStatus(t *testing.T) {
	client := NewClient(&fakePresenceRPCNode{
		response: presenceRPCResponse{Status: "mystery"},
	})

	err := client.CommitRoute(context.Background(), testPresenceTarget(), "pending-1")
	if err == nil {
		t.Fatal("CommitRoute() error = nil, want unknown status error")
	}
}

func testPresenceTarget() presence.RouteTarget {
	return presence.RouteTarget{
		HashSlot:       7,
		SlotID:         11,
		LeaderNodeID:   13,
		LeaderTerm:     17,
		ConfigEpoch:    18,
		RouteRevision:  17,
		AuthorityEpoch: 19,
	}
}

func testPresenceRoute(uid string, sessionID uint64) presence.Route {
	return presence.Route{
		UID:           uid,
		OwnerNodeID:   13,
		OwnerBootID:   23,
		OwnerSeq:      sessionID + 1000,
		SessionID:     sessionID,
		DeviceID:      fmt.Sprintf("device-%d", sessionID),
		DeviceFlag:    1,
		DeviceLevel:   2,
		Listener:      "tcp",
		ConnectedUnix: 1777777777 + int64(sessionID),
	}
}

func diffPresenceRPCRequest(got, want presenceRPCRequest) string {
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("got %#v want %#v", got, want)
	}
	return ""
}

func diffPresenceRPCResponse(got, want presenceRPCResponse) string {
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("got %#v want %#v", got, want)
	}
	return ""
}

type fakePresenceAuthority struct {
	registerErr     error
	registerCalls   []presenceRegisterCall
	commitCalls     []presenceTokenCall
	abortCalls      []presenceTokenCall
	unregisterCalls []presenceUnregisterCall
	endpointCalls   []presenceEndpointCall
	touchCalls      []presenceTouchCall
}

type fakeBatchPresenceAuthority struct {
	*fakePresenceAuthority
	batchCalls       []presence.EndpointLookupGroup
	errorsByHashSlot map[uint16]error
}

func (f *fakeBatchPresenceAuthority) EndpointsByUIDs(_ context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	f.batchCalls = append(f.batchCalls, presence.EndpointLookupGroup{Target: target, UIDs: append([]string(nil), uids...)})
	if err := f.errorsByHashSlot[target.HashSlot]; err != nil {
		return nil, err
	}
	routes := make([]presence.Route, 0, len(uids))
	for i, uid := range uids {
		routes = append(routes, testPresenceRoute(uid, uint64(101+i)))
	}
	return routes, nil
}

type fakePresenceOwner struct {
	actions []presence.RouteAction
}

func (f *fakePresenceOwner) ApplyRouteAction(_ context.Context, action presence.RouteAction) error {
	f.actions = append(f.actions, action)
	return nil
}

func newFakePresenceAuthority() *fakePresenceAuthority {
	return &fakePresenceAuthority{}
}

func (f *fakePresenceAuthority) RegisterRoute(_ context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	f.registerCalls = append(f.registerCalls, presenceRegisterCall{target: target, route: route})
	if f.registerErr != nil {
		return presence.RegisterResult{}, f.registerErr
	}
	return presence.RegisterResult{PendingToken: "pending-1"}, nil
}

func (f *fakePresenceAuthority) CommitRoute(_ context.Context, target presence.RouteTarget, token string) error {
	f.commitCalls = append(f.commitCalls, presenceTokenCall{target: target, token: token})
	return nil
}

func (f *fakePresenceAuthority) AbortRoute(_ context.Context, target presence.RouteTarget, token string) error {
	f.abortCalls = append(f.abortCalls, presenceTokenCall{target: target, token: token})
	return nil
}

func (f *fakePresenceAuthority) UnregisterRoute(_ context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	f.unregisterCalls = append(f.unregisterCalls, presenceUnregisterCall{target: target, identity: identity, ownerSeq: ownerSeq})
	return nil
}

func (f *fakePresenceAuthority) EndpointsByUID(_ context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	f.endpointCalls = append(f.endpointCalls, presenceEndpointCall{target: target, uid: uid})
	return []presence.Route{testPresenceRoute(uid, 101)}, nil
}

func (f *fakePresenceAuthority) TouchRoutes(_ context.Context, target presence.RouteTarget, routes []presence.Route) error {
	f.touchCalls = append(f.touchCalls, presenceTouchCall{target: target, routes: append([]presence.Route(nil), routes...)})
	return nil
}

type presenceRegisterCall struct {
	target presence.RouteTarget
	route  presence.Route
}

type presenceTokenCall struct {
	target presence.RouteTarget
	token  string
}

type presenceUnregisterCall struct {
	target   presence.RouteTarget
	identity presence.RouteIdentity
	ownerSeq uint64
}

type presenceEndpointCall struct {
	target presence.RouteTarget
	uid    string
}

type presenceTouchCall struct {
	target presence.RouteTarget
	routes []presence.Route
}

type fakePresenceRPCNode struct {
	response  presenceRPCResponse
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type fakePresenceBatchRPCNode struct {
	results   []presenceRPCEndpointLookupResult
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type rollingUpgradePresenceRPCNode struct {
	adapter            *Adapter
	batchServiceCalls  int
	legacyServiceCalls int
}

type rejectingPresenceRPCNode struct {
	err   error
	calls int
}

func (n *rejectingPresenceRPCNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	n.calls++
	return nil, n.err
}

func (n *rollingUpgradePresenceRPCNode) CallRPC(ctx context.Context, _ uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if serviceID != PresenceAuthorityRPCServiceID {
		return nil, fmt.Errorf("unexpected service %d", serviceID)
	}
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		return nil, err
	}
	if req.Op == presenceOpEndpointsByTargets {
		n.batchServiceCalls++
		return nil, transport.RemoteError{
			Code:    "remote_error",
			Message: "internal/access/node: unknown presence op id 8",
		}
	}
	n.legacyServiceCalls++
	return n.adapter.HandlePresenceAuthorityRPC(ctx, payload)
}

func (f *fakePresenceBatchRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return encodePresenceEndpointsByTargetsResponseBinary(f.results)
}

func (f *fakePresenceRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return encodePresenceRPCResponseBinary(f.response)
}
