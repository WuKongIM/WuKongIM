package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityRPCDispatchesList(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	local := &fakeConversationAuthority{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := New(Options{ConversationAuthority: local})
	req := conversationAuthorityRequest{Op: conversationOpList, Target: target, UID: "u1", Limit: 10}
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
}

func TestConversationAuthorityRPCDispatchesAdmitAndDrain(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	patches := []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 9}}
	local := &fakeConversationAuthority{drainResult: conversationDrainResultTransferred}
	adapter := New(Options{ConversationAuthority: local})

	admitBody, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpAdmitPatches, Target: target, Patches: patches})
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

	drainBody, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpDrain, Target: target})
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
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	batch := conversationactive.ActiveBatch{
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

	body, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpAdmitActiveBatch, Target: target, ActiveBatch: batch})
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

func TestConversationAuthorityClientCallsExpectedServiceAndMapsStatus(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	after := metadb.UserConversationActiveCursor{ActiveAt: 99, ChannelID: "g0", ChannelType: 2}
	node := &fakeConversationRPCNode{response: conversationAuthorityResponse{Status: conversationRPCStatusCachePressure}}
	client := NewClient(node)

	_, err := client.ListConversations(context.Background(), 13, target, "u1", after, 10)
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
	if req.Op != conversationOpList || req.UID != "u1" || req.Limit != 10 {
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
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	batch := conversationactive.ActiveBatch{
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

func TestConversationAuthorityClientChunksOversizedAdmitPayloads(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	patches := make([]conversationusecase.ActivePatch, maxConversationAuthorityCollectionLen+1)
	for i := range patches {
		patches[i] = conversationusecase.ActivePatch{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: int64(i + 1), MessageSeq: uint64(i + 1),
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
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
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
				return client.AdmitConversationActiveBatch(context.Background(), 13, target, conversationactive.ActiveBatch{SenderUID: "sender"})
			},
		},
		{
			name: "list",
			call: func(client *Client) error {
				_, err := client.ListConversations(context.Background(), 13, target, "u1", metadb.UserConversationActiveCursor{}, 10)
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
	target       conversationusecase.RouteTarget
	admitTarget  conversationusecase.RouteTarget
	activeTarget conversationusecase.RouteTarget
	drainTarget  conversationusecase.RouteTarget
	patches      []conversationusecase.ActivePatch
	activeBatch  conversationactive.ActiveBatch
	page         conversationusecase.ActiveViewPage
	drainResult  string
}

func (f *fakeConversationAuthority) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	f.admitTarget = target
	f.patches = append([]conversationusecase.ActivePatch(nil), patches...)
	return nil
}

func (f *fakeConversationAuthority) AdmitActiveBatch(_ context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	f.activeTarget = target
	f.activeBatch = batch
	return nil
}

func (f *fakeConversationAuthority) ListUserConversationActiveViewForTarget(_ context.Context, target conversationusecase.RouteTarget, _ string, _ metadb.UserConversationActiveCursor, _ int) (conversationusecase.ActiveViewPage, error) {
	f.target = target
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
