package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

func TestRecipientAuthorityRPCHandlerDispatchesLocalProcessor(t *testing.T) {
	req := recipientAuthorityTestProcessRequest()
	processor := &fakeRecipientAuthorityProcessor{}
	adapter := New(Options{RecipientAuthority: processor})
	body, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{
		Target:     req.Target,
		Event:      req.Event,
		Recipients: req.Recipients,
	})
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}

	respBody, err := adapter.HandleRecipientAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRecipientAuthorityRPC() error = %v", err)
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if !reflect.DeepEqual(processor.req, req) {
		t.Fatalf("request = %#v, want %#v", processor.req, req)
	}
}

func TestRecipientAuthorityRPCHandlerMapsProcessorErrors(t *testing.T) {
	req := recipientAuthorityTestProcessRequest()
	body, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{
		Target:     req.Target,
		Event:      req.Event,
		Recipients: req.Recipients,
	})
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}
	adapter := New(Options{RecipientAuthority: &fakeRecipientAuthorityProcessor{err: recipientusecase.ErrStaleRoute}})

	respBody, err := adapter.HandleRecipientAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRecipientAuthorityRPC() error = %v", err)
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityResponse() error = %v", err)
	}
	if resp.Status != rpcStatusStaleRoute {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusStaleRoute)
	}
}

func TestRecipientAuthorityRPCHandlerRejectsNilProcessor(t *testing.T) {
	req := recipientAuthorityTestProcessRequest()
	body, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{
		Target:     req.Target,
		Event:      req.Event,
		Recipients: req.Recipients,
	})
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}

	respBody, err := New(Options{}).HandleRecipientAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRecipientAuthorityRPC() error = %v", err)
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusRejected)
	}
}

func TestRecipientAuthorityClientCallsExpectedServiceAndMapsStatus(t *testing.T) {
	req := recipientAuthorityTestProcessRequest()
	node := &fakeRecipientAuthorityRPCNode{response: recipientAuthorityResponse{Status: rpcStatusRouteNotReady}}
	client := NewClient(node)

	err := client.ProcessRecipientAuthority(context.Background(), req.Target.LeaderNodeID, req)

	if !errors.Is(err, recipientusecase.ErrRouteNotReady) {
		t.Fatalf("ProcessRecipientAuthority() error = %v, want %v", err, recipientusecase.ErrRouteNotReady)
	}
	if node.nodeID != req.Target.LeaderNodeID {
		t.Fatalf("nodeID = %d, want %d", node.nodeID, req.Target.LeaderNodeID)
	}
	if node.serviceID != RecipientAuthorityRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, RecipientAuthorityRPCServiceID)
	}
	got, err := decodeRecipientAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityRequest(client payload) error = %v", err)
	}
	want := recipientAuthorityRequest{Target: req.Target, Event: req.Event, Recipients: req.Recipients}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rpc request = %#v, want %#v", got, want)
	}
}

func TestRecipientAuthorityClientRejectsNilNodeAndUnknownStatus(t *testing.T) {
	req := recipientAuthorityTestProcessRequest()
	if err := NewClient(nil).ProcessRecipientAuthority(context.Background(), req.Target.LeaderNodeID, req); err == nil {
		t.Fatal("ProcessRecipientAuthority(nil node) error = nil, want error")
	}

	err := NewClient(&fakeRecipientAuthorityRPCNode{response: recipientAuthorityResponse{Status: "mystery"}}).
		ProcessRecipientAuthority(context.Background(), req.Target.LeaderNodeID, req)
	if err == nil {
		t.Fatal("ProcessRecipientAuthority(unknown status) error = nil, want error")
	}
}

func TestRecipientAuthorityRPCStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status string
		want   error
	}{
		{name: "ok", status: rpcStatusOK},
		{name: "not leader", err: recipientusecase.ErrNotLeader, status: rpcStatusNotLeader, want: recipientusecase.ErrNotLeader},
		{name: "stale route", err: recipientusecase.ErrStaleRoute, status: rpcStatusStaleRoute, want: recipientusecase.ErrStaleRoute},
		{name: "route not ready", err: recipientusecase.ErrRouteNotReady, status: rpcStatusRouteNotReady, want: recipientusecase.ErrRouteNotReady},
		{name: "rejected", err: errors.New("boom"), status: rpcStatusRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recipientAuthorityRPCStatusForError(tt.err); got != tt.status {
				t.Fatalf("status = %q, want %q", got, tt.status)
			}
			err := recipientAuthorityRPCErrorForStatus(tt.status)
			if tt.want == nil {
				if tt.status == rpcStatusOK && err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				if tt.status == rpcStatusRejected && err == nil {
					t.Fatal("error = nil, want rejected error")
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func recipientAuthorityTestProcessRequest() recipientusecase.ProcessRequest {
	return recipientusecase.ProcessRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 9, RouteRevision: 4, AuthorityEpoch: 5},
		Event: messageevents.MessageCommitted{
			MessageID:         1001,
			MessageSeq:        42,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			SenderNodeID:      7,
			SenderSessionID:   8,
			ClientMsgNo:       "client-1",
			ServerTimestampMS: 9000,
			Payload:           []byte("hello"),
			RedDot:            true,
			MessageScopedUIDs: []string{"u2"},
		},
		Recipients: []recipientusecase.Recipient{{UID: "u2", JoinSeq: 10}},
	}
}

type fakeRecipientAuthorityProcessor struct {
	req recipientusecase.ProcessRequest
	err error
}

func (f *fakeRecipientAuthorityProcessor) Process(_ context.Context, req recipientusecase.ProcessRequest) error {
	req.Event = req.Event.Clone()
	req.Recipients = append([]recipientusecase.Recipient(nil), req.Recipients...)
	f.req = req
	return f.err
}

type fakeRecipientAuthorityRPCNode struct {
	response  recipientAuthorityResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeRecipientAuthorityRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	return encodeRecipientAuthorityResponse(f.response)
}
