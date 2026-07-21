package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestConversationAuthorityRPCDispatchesList(t *testing.T) {
	target := testConversationAuthorityTarget()
	local := &fakeConversationAuthority{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := New(Options{ConversationAuthority: local})
	req := conversationAuthorityRequest{Op: conversationOpList, Target: target, Kind: metadb.ConversationKindCMD, UID: "u1", Limit: 10}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC() error = %v", err)
	}
	resp, err := decodeConversationAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != conversationRPCStatusOK || len(resp.Page.Rows) != 1 {
		t.Fatalf("resp = %#v, want ok with row", resp)
	}
	if local.target != target {
		t.Fatalf("target = %#v, want %#v", local.target, target)
	}
	if local.listKind != metadb.ConversationKindCMD {
		t.Fatalf("list kind = %v, want %v", local.listKind, metadb.ConversationKindCMD)
	}
}

func TestConversationAuthorityRPCDispatchesAdmitAndDrain(t *testing.T) {
	target := testConversationAuthorityTarget()
	patches := []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 9}}
	local := &fakeConversationAuthority{drainResult: conversationDrainResultTransferred}
	adapter := New(Options{ConversationAuthority: local})

	admitBody, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpAdmitPatches, Target: target, Kind: metadb.ConversationKindNormal, Patches: patches})
	if err != nil {
		t.Fatalf("encode admit request: %v", err)
	}
	admitRespBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), admitBody)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(admit) error = %v", err)
	}
	admitResp, err := decodeConversationAuthorityResponse(admitRespBody)
	if err != nil {
		t.Fatalf("decode admit response: %v", err)
	}
	if admitResp.Status != conversationRPCStatusOK {
		t.Fatalf("admit status = %q, want %q", admitResp.Status, conversationRPCStatusOK)
	}
	if local.admitTarget != target || !reflect.DeepEqual(local.patches, patches) {
		t.Fatalf("admit target/patches = %#v/%#v, want %#v/%#v", local.admitTarget, local.patches, target, patches)
	}

	drainBody, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpDrain, Target: target, Kind: metadb.ConversationKindNormal})
	if err != nil {
		t.Fatalf("encode drain request: %v", err)
	}
	drainRespBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), drainBody)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(drain) error = %v", err)
	}
	drainResp, err := decodeConversationAuthorityResponse(drainRespBody)
	if err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if drainResp.Status != conversationRPCStatusOK || drainResp.DrainResult != conversationDrainResultTransferred {
		t.Fatalf("drain response = %#v", drainResp)
	}
	if local.drainTarget != target {
		t.Fatalf("drain target = %#v, want %#v", local.drainTarget, target)
	}
}

func TestConversationAuthorityRPCDispatchesAdmitActiveBatch(t *testing.T) {
	target := testConversationAuthorityTarget()
	batch := conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients: []conversationactive.ActiveEntry{
			{UID: "sender", IsSender: true},
			{UID: "receiver"},
		},
	}
	local := &fakeConversationAuthority{}
	adapter := New(Options{ConversationAuthority: local})

	body, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpAdmitActiveBatch, Target: target, Kind: metadb.ConversationKindNormal, ActiveBatch: batch})
	if err != nil {
		t.Fatalf("encode active batch request: %v", err)
	}
	respBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(active batch) error = %v", err)
	}
	resp, err := decodeConversationAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decode active batch response: %v", err)
	}
	if resp.Status != conversationRPCStatusOK {
		t.Fatalf("active batch status = %q, want %q", resp.Status, conversationRPCStatusOK)
	}
	if local.activeTarget != target || !reflect.DeepEqual(local.activeBatch, batch) {
		t.Fatalf("active target/batch = %#v/%#v, want %#v/%#v", local.activeTarget, local.activeBatch, target, batch)
	}
}

func TestConversationAuthorityRPCDispatchesBulkActiveBatchesWithAlignedResults(t *testing.T) {
	target := testConversationAuthorityTarget()
	secondTarget := target
	secondTarget.HashSlot = 2
	groups := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender", ChannelID: "g1"}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "g1", Recipients: []conversationactive.ActiveEntry{{UID: "u1"}}}},
	}
	local := &fakeConversationBatchAuthority{results: []ConversationActiveBatchResult{
		{},
		{Err: conversationusecase.ErrStaleRoute},
	}}
	adapter := New(Options{ConversationAuthority: local})
	body, err := encodeConversationActiveBatchGroups(groups)
	if err != nil {
		t.Fatalf("encode bulk request: %v", err)
	}
	responseBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(bulk) error = %v", err)
	}
	wireResults, err := decodeConversationActiveBatchResults(responseBody)
	if err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if local.calls != 1 || !reflect.DeepEqual(local.groups, groups) {
		t.Fatalf("bulk authority calls/groups = %d/%#v, want one/%#v", local.calls, local.groups, groups)
	}
	wantResults := []conversationActiveBatchWireResult{
		{Status: conversationRPCStatusOK},
		{Status: conversationRPCStatusStaleRoute},
	}
	if !reflect.DeepEqual(wireResults, wantResults) {
		t.Fatalf("wire results = %#v, want %#v", wireResults, wantResults)
	}
}

func TestConversationAuthorityRPCBulkFallsBackToLegacyAuthorityWithAlignedResults(t *testing.T) {
	target := testConversationAuthorityTarget()
	secondTarget := target
	secondTarget.HashSlot = 2
	groups := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender", ChannelID: "g1"}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "g1", Recipients: []conversationactive.ActiveEntry{{UID: "u1"}}}},
	}
	local := &fakeConversationAuthority{activeErrors: []error{nil, conversationusecase.ErrCachePressure}}
	adapter := New(Options{ConversationAuthority: local})
	body, err := encodeConversationActiveBatchGroups(groups)
	if err != nil {
		t.Fatalf("encode bulk request: %v", err)
	}
	responseBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(bulk legacy) error = %v", err)
	}
	wireResults, err := decodeConversationActiveBatchResults(responseBody)
	if err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if !reflect.DeepEqual(local.activeTargets, []conversationusecase.RouteTarget{target, secondTarget}) ||
		!reflect.DeepEqual(local.activeBatches, []conversationactive.ActiveBatch{groups[0].Batch, groups[1].Batch}) {
		t.Fatalf("legacy active calls = %#v/%#v", local.activeTargets, local.activeBatches)
	}
	wantResults := []conversationActiveBatchWireResult{
		{Status: conversationRPCStatusOK},
		{Status: conversationRPCStatusCachePressure},
	}
	if !reflect.DeepEqual(wireResults, wantResults) {
		t.Fatalf("wire results = %#v, want %#v", wireResults, wantResults)
	}
}

func TestConversationAuthorityRPCDispatchesHideConversations(t *testing.T) {
	target := testConversationAuthorityTarget()
	deletes := []metadb.ConversationDelete{
		{UID: "u2", Kind: metadb.ConversationKindCMD, ChannelID: "g2____cmd", ChannelType: 3, DeletedToSeq: 19, UpdatedAt: 102},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 9, UpdatedAt: 101},
	}
	local := &fakeConversationAuthority{hideErr: conversationusecase.ErrStaleRoute}
	adapter := New(Options{ConversationAuthority: local})
	body, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{
		Op:      conversationOpHideConversations,
		Target:  target,
		Kind:    metadb.ConversationKindNormal,
		Deletes: deletes,
	})
	if err != nil {
		t.Fatalf("encode hide request: %v", err)
	}
	respBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC(hide) error = %v", err)
	}
	resp, err := decodeConversationAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decode hide response: %v", err)
	}
	if resp.Status != conversationRPCStatusStaleRoute {
		t.Fatalf("hide status = %q, want %q", resp.Status, conversationRPCStatusStaleRoute)
	}
	if local.hideTarget != target || !reflect.DeepEqual(local.deletes, deletes) {
		t.Fatalf("hide target/deletes = %#v/%#v, want %#v/%#v", local.hideTarget, local.deletes, target, deletes)
	}
}

func TestConversationAuthorityClientCallsExpectedServiceAndMapsStatus(t *testing.T) {
	target := testConversationAuthorityTarget()
	after := metadb.ConversationActiveCursor{ActiveAt: 99, ChannelID: "g0____cmd", ChannelType: 2}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusCachePressure}}
	client := NewClient(node)

	_, err := client.ListConversations(context.Background(), 13, target, metadb.ConversationKindCMD, "u1", after, 10)
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("ListConversations() error = %v, want ErrCachePressure", err)
	}
	if node.nodeID != 13 {
		t.Fatalf("nodeID = %d, want 13", node.nodeID)
	}
	if node.serviceID != ConversationAuthorityRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, ConversationAuthorityRPCServiceID)
	}
	req, err := decodeConversationAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest(client payload) error = %v", err)
	}
	if req.Op != conversationOpList || req.Kind != metadb.ConversationKindCMD || req.UID != "u1" || req.Limit != 10 {
		t.Fatalf("client request = %#v", req)
	}
	if !reflect.DeepEqual(req.Target, target) {
		t.Fatalf("target = %#v, want %#v", req.Target, target)
	}
	if !reflect.DeepEqual(req.After, after) {
		t.Fatalf("after = %#v, want %#v", req.After, after)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchMapsStatus(t *testing.T) {
	target := testConversationAuthorityTarget()
	batch := conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusCachePressure}}
	client := NewClient(node)

	err := client.AdmitConversationActiveBatch(context.Background(), 13, target, batch)
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitConversationActiveBatch() error = %v, want ErrCachePressure", err)
	}
	if node.nodeID != 13 {
		t.Fatalf("nodeID = %d, want 13", node.nodeID)
	}
	if node.serviceID != ConversationAuthorityRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, ConversationAuthorityRPCServiceID)
	}
	req, err := decodeConversationAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest(client payload) error = %v", err)
	}
	if req.Op != conversationOpAdmitActiveBatch || !reflect.DeepEqual(req.ActiveBatch, batch) {
		t.Fatalf("client active batch request = %#v, want op %q batch %#v", req, conversationOpAdmitActiveBatch, batch)
	}
	if !reflect.DeepEqual(req.Target, target) {
		t.Fatalf("target = %#v, want %#v", req.Target, target)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesPreservesAlignedPartialFailures(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	secondTarget := target
	secondTarget.HashSlot = 2
	groups := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender", ChannelID: "g1"}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "g1", Recipients: []conversationactive.ActiveEntry{{UID: "u1"}}}},
	}
	node := &fakeConversationBulkRPCNode{results: []conversationActiveBatchWireResult{
		{Status: conversationRPCStatusOK},
		{Status: conversationRPCStatusNotLeader},
	}}
	client := NewClient(node)

	results, err := client.AdmitConversationActiveBatches(context.Background(), 13, groups)
	if err != nil {
		t.Fatalf("AdmitConversationActiveBatches() error = %v", err)
	}
	if len(results) != 2 || results[0].Err != nil || !errors.Is(results[1].Err, conversationusecase.ErrNotLeader) {
		t.Fatalf("results = %#v", results)
	}
	if node.nodeID != 13 || node.serviceID != ConversationAuthorityRPCServiceID || node.calls != 1 {
		t.Fatalf("rpc destination/calls = node %d service %d calls %d", node.nodeID, node.serviceID, node.calls)
	}
	gotGroups, err := decodeConversationActiveBatchGroups(node.payload)
	if err != nil {
		t.Fatalf("decode client bulk payload: %v", err)
	}
	if !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("client bulk groups = %#v, want %#v", gotGroups, groups)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesRejectsDestinationMismatchAndMisalignedResponse(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	groups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch:  conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"},
	}}
	node := &fakeConversationBulkRPCNode{results: []conversationActiveBatchWireResult{{Status: conversationRPCStatusOK}}}
	client := NewClient(node)
	if _, err := client.AdmitConversationActiveBatches(context.Background(), 14, groups); err == nil {
		t.Fatal("AdmitConversationActiveBatches(destination mismatch) error = nil, want error")
	}
	if node.calls != 0 {
		t.Fatalf("transport calls after destination mismatch = %d, want zero", node.calls)
	}

	groups = append(groups, groups[0])
	if _, err := client.AdmitConversationActiveBatches(context.Background(), 13, groups); err == nil {
		t.Fatal("AdmitConversationActiveBatches(misaligned response) error = nil, want error")
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesFallsBackOnceAndCachesUnsupportedPeer(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	secondTarget := target
	secondTarget.HashSlot = 2
	groups := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "u1"}}}},
	}
	authority := &fakeConversationAuthority{}
	node := &rollingUpgradeConversationRPCNode{adapter: New(Options{ConversationAuthority: authority})}
	client := NewClient(node)
	for attempt := 0; attempt < 2; attempt++ {
		results, err := client.AdmitConversationActiveBatches(context.Background(), 13, groups)
		if err != nil {
			t.Fatalf("AdmitConversationActiveBatches(attempt %d) error = %v", attempt+1, err)
		}
		if len(results) != len(groups) || results[0].Err != nil || results[1].Err != nil {
			t.Fatalf("attempt %d results = %#v", attempt+1, results)
		}
	}
	if node.bulkCalls != 1 || node.legacyCalls != 4 {
		t.Fatalf("bulk/legacy calls = %d/%d, want 1/4", node.bulkCalls, node.legacyCalls)
	}
	if len(authority.activeTargets) != 4 {
		t.Fatalf("authority legacy calls = %d, want 4", len(authority.activeTargets))
	}
	overRows := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: make([]conversationactive.ActiveEntry, maxConversationAuthorityCollectionLen/2)}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: make([]conversationactive.ActiveEntry, maxConversationAuthorityCollectionLen/2+1)}},
	}
	if _, err := client.AdmitConversationActiveBatches(context.Background(), 13, overRows); err == nil {
		t.Fatal("AdmitConversationActiveBatches(cached unsupported oversized) error = nil, want error")
	}
	if node.bulkCalls != 1 || node.legacyCalls != 4 {
		t.Fatalf("oversized cached call reached transport: bulk/legacy = %d/%d", node.bulkCalls, node.legacyCalls)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesDoesNotFallbackForOtherErrors(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	groups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch:  conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"},
	}}
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "timeout",
			err:  context.DeadlineExceeded,
		},
		{
			name: "ordinary transport error",
			err:  errors.New("connection reset"),
		},
		{
			name: "different remote code",
			err: transport.RemoteError{
				Code:    "permission_denied",
				Message: conversationAuthorityV1InvalidRequestCodecMessage,
			},
		},
		{
			name: "similar remote message",
			err: transport.RemoteError{
				Code:    "remote_error",
				Message: "proxy rejected: invalid conversation authority request codec",
			},
		},
		{
			name: "non remote error",
			err:  errors.New(conversationAuthorityV1InvalidRequestCodecMessage),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeConversationBulkRPCNode{err: tt.err}
			results, err := NewClient(node).AdmitConversationActiveBatches(context.Background(), 13, groups)
			if err == nil || err.Error() != tt.err.Error() {
				t.Fatalf("AdmitConversationActiveBatches() error = %v, want %v", err, tt.err)
			}
			if results != nil {
				t.Fatalf("results = %#v, want nil", results)
			}
			if node.calls != 1 {
				t.Fatalf("transport calls = %d, want one", node.calls)
			}
		})
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesMapsTransportCancellation(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	groups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch:  conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"},
	}}
	node := &fakeConversationBulkRPCNode{err: transport.ErrCanceled}

	results, err := NewClient(node).AdmitConversationActiveBatches(context.Background(), 13, groups)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AdmitConversationActiveBatches() error = %v, want context.Canceled", err)
	}
	if results != nil {
		t.Fatalf("results = %#v, want nil", results)
	}
	if node.calls != 1 {
		t.Fatalf("transport calls = %d, want one without fallback", node.calls)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchesDoesNotFallbackForInvalidV2Response(t *testing.T) {
	target := testConversationAuthorityTarget()
	target.LeaderNodeID = 13
	groups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch:  conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"},
	}}
	node := &fakeConversationBulkRPCNode{rawResponse: []byte("invalid WKVc2")}
	results, err := NewClient(node).AdmitConversationActiveBatches(context.Background(), 13, groups)
	if err == nil {
		t.Fatal("AdmitConversationActiveBatches() error = nil, want invalid response codec error")
	}
	if results != nil {
		t.Fatalf("results = %#v, want nil", results)
	}
	if node.calls != 1 {
		t.Fatalf("transport calls = %d, want one without fallback", node.calls)
	}
}

func TestConversationAuthorityClientHideConversationsMapsStatus(t *testing.T) {
	target := testConversationAuthorityTarget()
	deletes := []metadb.ConversationDelete{
		{UID: "u2", Kind: metadb.ConversationKindCMD, ChannelID: "g2____cmd", ChannelType: 3, DeletedToSeq: 19, UpdatedAt: 102},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 9, UpdatedAt: 101},
	}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusRouteNotReady}}
	client := NewClient(node)

	err := client.HideConversations(context.Background(), 13, target, deletes)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("HideConversations() error = %v, want ErrRouteNotReady", err)
	}
	if node.nodeID != 13 || node.serviceID != ConversationAuthorityRPCServiceID {
		t.Fatalf("rpc target = node %d service %d, want node 13 service %d", node.nodeID, node.serviceID, ConversationAuthorityRPCServiceID)
	}
	req, err := decodeConversationAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest(client hide payload) error = %v", err)
	}
	if req.Op != conversationOpHideConversations || req.Target != target || !reflect.DeepEqual(req.Deletes, deletes) {
		t.Fatalf("client hide request = %#v, want target %#v deletes %#v", req, target, deletes)
	}
}

func TestConversationAuthorityClientRejectsOversizedHideCollectionBeforeTransport(t *testing.T) {
	deletes := make([]metadb.ConversationDelete, maxConversationAuthorityCollectionLen+1)
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusOK}}
	err := NewClient(node).HideConversations(context.Background(), 13, testConversationAuthorityTarget(), deletes)
	if err == nil {
		t.Fatal("HideConversations() error = nil, want collection limit error")
	}
	if len(node.payloads) != 0 {
		t.Fatalf("rpc payload count = %d, want 0", len(node.payloads))
	}
}

func TestConversationAuthorityClientAcceptsMaximumHideCollection(t *testing.T) {
	deletes := make([]metadb.ConversationDelete, maxConversationAuthorityCollectionLen)
	for i := range deletes {
		deletes[i] = metadb.ConversationDelete{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: uint64(i + 1), UpdatedAt: int64(i + 1),
		}
	}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusOK}}
	if err := NewClient(node).HideConversations(context.Background(), 13, testConversationAuthorityTarget(), deletes); err != nil {
		t.Fatalf("HideConversations() error = %v", err)
	}
	if len(node.payloads) != 1 {
		t.Fatalf("rpc payload count = %d, want 1", len(node.payloads))
	}
	req, err := decodeConversationAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest(maximum hide payload) error = %v", err)
	}
	if !reflect.DeepEqual(req.Deletes, deletes) {
		t.Fatalf("decoded delete count/content mismatch: got %d entries, want %d", len(req.Deletes), len(deletes))
	}
}

func TestConversationAuthorityClientChunksOversizedAdmitPayloads(t *testing.T) {
	target := testConversationAuthorityTarget()
	patches := make([]conversationusecase.ActivePatch, maxConversationAuthorityCollectionLen+1)
	for i := range patches {
		patches[i] = conversationusecase.ActivePatch{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: int64(i + 1), MessageSeq: uint64(i + 1),
		}
	}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusOK}}
	client := NewClient(node)

	if err := client.AdmitConversationPatches(context.Background(), 13, target, patches); err != nil {
		t.Fatalf("AdmitConversationPatches() error = %v", err)
	}
	if len(node.payloads) != 2 {
		t.Fatalf("rpc payload count = %d, want two chunks", len(node.payloads))
	}
	first, err := decodeConversationAuthorityRequest(node.payloads[0])
	if err != nil {
		t.Fatalf("decode first chunk: %v", err)
	}
	second, err := decodeConversationAuthorityRequest(node.payloads[1])
	if err != nil {
		t.Fatalf("decode second chunk: %v", err)
	}
	if len(first.Patches) != maxConversationAuthorityCollectionLen || len(second.Patches) != 1 {
		t.Fatalf("chunk sizes = %d/%d, want %d/1", len(first.Patches), len(second.Patches), maxConversationAuthorityCollectionLen)
	}
}

func TestConversationAuthorityClientRejectsNilNodeAndUnknownStatus(t *testing.T) {
	target := testConversationAuthorityTarget()
	calls := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "admit",
			call: func(client *Client) error {
				return client.AdmitConversationPatches(context.Background(), 13, target, nil)
			},
		},
		{
			name: "active batch",
			call: func(client *Client) error {
				return client.AdmitConversationActiveBatch(context.Background(), 13, target, conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "sender"})
			},
		},
		{
			name: "hide",
			call: func(client *Client) error {
				return client.HideConversations(context.Background(), 13, target, nil)
			},
		},
		{
			name: "list",
			call: func(client *Client) error {
				_, err := client.ListConversations(context.Background(), 13, target, metadb.ConversationKindCMD, "u1", metadb.ConversationActiveCursor{}, 10)
				return err
			},
		},
		{
			name: "drain",
			call: func(client *Client) error {
				_, err := client.DrainConversationAuthority(context.Background(), 13, target)
				return err
			},
		},
	}
	for _, tt := range calls {
		t.Run(tt.name+" nil client", func(t *testing.T) {
			if err := tt.call(nil); err == nil {
				t.Fatal("client error = nil, want error")
			}
		})
		t.Run(tt.name+" nil node", func(t *testing.T) {
			if err := tt.call(NewClient(nil)); err == nil {
				t.Fatal("client error = nil, want error")
			}
		})
		t.Run(tt.name+" unknown status", func(t *testing.T) {
			client := NewClient(&fakeConversationRPCNode{response: conversationAuthorityResponse{Status: "mystery"}})
			if err := tt.call(client); err == nil {
				t.Fatal("client error = nil, want unknown status error")
			}
		})
	}
}

func TestConversationAuthorityRPCStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status string
	}{
		{name: "ok", status: conversationRPCStatusOK},
		{name: "not leader", err: conversationusecase.ErrNotLeader, status: conversationRPCStatusNotLeader},
		{name: "stale route", err: conversationusecase.ErrStaleRoute, status: conversationRPCStatusStaleRoute},
		{name: "route not ready", err: conversationusecase.ErrRouteNotReady, status: conversationRPCStatusRouteNotReady},
		{name: "cache pressure", err: conversationusecase.ErrCachePressure, status: conversationRPCStatusCachePressure},
		{name: "context canceled", err: context.Canceled, status: conversationRPCStatusCanceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, status: conversationRPCStatusDeadline},
		{name: "rejected", err: errors.New("boom"), status: conversationRPCStatusRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conversationRPCStatusForError(tt.err); got != tt.status {
				t.Fatalf("conversationRPCStatusForError() = %q, want %q", got, tt.status)
			}
			gotErr := conversationRPCErrorForStatus(tt.status)
			if tt.err == nil && gotErr != nil {
				t.Fatalf("conversationRPCErrorForStatus() error = %v, want nil", gotErr)
			}
			if tt.err != nil && tt.status != conversationRPCStatusRejected && !errors.Is(gotErr, tt.err) {
				t.Fatalf("conversationRPCErrorForStatus() error = %v, want %v", gotErr, tt.err)
			}
			if tt.status == conversationRPCStatusRejected && gotErr == nil {
				t.Fatal("conversationRPCErrorForStatus(rejected) error = nil, want error")
			}
		})
	}
}

type fakeConversationAuthority struct {
	target        conversationusecase.RouteTarget
	admitTarget   conversationusecase.RouteTarget
	activeTarget  conversationusecase.RouteTarget
	activeTargets []conversationusecase.RouteTarget
	hideTarget    conversationusecase.RouteTarget
	drainTarget   conversationusecase.RouteTarget
	patches       []conversationusecase.ActivePatch
	activeBatch   conversationactive.ActiveBatch
	activeBatches []conversationactive.ActiveBatch
	activeErrors  []error
	deletes       []metadb.ConversationDelete
	page          conversationusecase.ActiveViewPage
	drainResult   string
	listKind      metadb.ConversationKind
	hideErr       error
}

func testConversationAuthorityTarget() conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       1,
		SlotID:         2,
		LeaderNodeID:   3,
		LeaderTerm:     6,
		ConfigEpoch:    7,
		RouteRevision:  4,
		AuthorityEpoch: 5,
	}
}

func (f *fakeConversationAuthority) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	f.admitTarget = target
	f.patches = append([]conversationusecase.ActivePatch(nil), patches...)
	return nil
}

func (f *fakeConversationAuthority) AdmitActiveBatch(_ context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	f.activeTarget = target
	f.activeBatch = batch
	f.activeTargets = append(f.activeTargets, target)
	f.activeBatches = append(f.activeBatches, batch)
	call := len(f.activeTargets) - 1
	if call < len(f.activeErrors) {
		return f.activeErrors[call]
	}
	return nil
}

func (f *fakeConversationAuthority) HideConversationsForTarget(_ context.Context, target conversationusecase.RouteTarget, deletes []metadb.ConversationDelete) error {
	f.hideTarget = target
	f.deletes = append([]metadb.ConversationDelete(nil), deletes...)
	return f.hideErr
}

func (f *fakeConversationAuthority) ListConversationActiveViewForTarget(_ context.Context, target conversationusecase.RouteTarget, kind metadb.ConversationKind, _ string, _ metadb.ConversationActiveCursor, _ int) (conversationusecase.ActiveViewPage, error) {
	f.target = target
	f.listKind = kind
	return f.page, nil
}

func (f *fakeConversationAuthority) DrainAuthority(_ context.Context, target conversationusecase.RouteTarget) (string, error) {
	f.drainTarget = target
	if f.drainResult == "" {
		return conversationDrainResultNoDirty, nil
	}
	return f.drainResult, nil
}

type fakeConversationRPCNode struct {
	response  conversationAuthorityResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
	payloads  [][]byte
}

type fakeConversationBatchAuthority struct {
	fakeConversationAuthority
	groups  []ConversationActiveBatchGroup
	results []ConversationActiveBatchResult
	calls   int
}

func (f *fakeConversationBatchAuthority) AdmitActiveBatches(_ context.Context, groups []ConversationActiveBatchGroup) []ConversationActiveBatchResult {
	f.calls++
	f.groups = append([]ConversationActiveBatchGroup(nil), groups...)
	return append([]ConversationActiveBatchResult(nil), f.results...)
}

type fakeConversationBulkRPCNode struct {
	results     []conversationActiveBatchWireResult
	rawResponse []byte
	err         error
	nodeID      uint64
	serviceID   uint8
	payload     []byte
	calls       int
}

func (f *fakeConversationBulkRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls++
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	if f.rawResponse != nil {
		return append([]byte(nil), f.rawResponse...), nil
	}
	return encodeConversationActiveBatchResults(f.results)
}

type rollingUpgradeConversationRPCNode struct {
	adapter     *Adapter
	bulkCalls   int
	legacyCalls int
}

func (n *rollingUpgradeConversationRPCNode) CallRPC(ctx context.Context, _ uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if serviceID != ConversationAuthorityRPCServiceID {
		return nil, fmt.Errorf("unexpected service %d", serviceID)
	}
	if hasMagic(payload, conversationAuthorityBatchRequestMagic[:]) {
		n.bulkCalls++
		return nil, transport.RemoteError{
			Code:    "remote_error",
			Message: conversationAuthorityV1InvalidRequestCodecMessage,
		}
	}
	n.legacyCalls++
	return n.adapter.HandleConversationAuthorityRPC(ctx, payload)
}

func (f *fakeConversationRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	f.payloads = append(f.payloads, append([]byte(nil), payload...))
	if f.err != nil {
		return nil, f.err
	}
	return encodeConversationAuthorityResponse(f.response)
}
