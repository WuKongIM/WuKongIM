package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

func TestSenderAuthorityRPCHandlerDispatchesLocalSubmitter(t *testing.T) {
	target := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	cmd := senderAuthorityTestCommand()
	result := message.SendBatchItemResult{Result: message.SendResult{MessageID: 1001, MessageSeq: 10, Reason: message.ReasonSuccess}}
	submitter := &fakeSenderAuthoritySubmitter{results: []message.SendBatchItemResult{result}}
	adapter := New(Options{SenderAuthority: submitter})
	body, err := encodeSenderAuthorityRequest(senderAuthorityRequest{
		Target: target,
		Items:  []senderAuthorityItem{{Command: cmd, Timeout: time.Second}},
	})
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}
	before := time.Now()

	respBody, err := adapter.HandleSenderAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleSenderAuthorityRPC() error = %v", err)
	}
	resp, err := decodeSenderAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if !reflect.DeepEqual(resp.Results, []message.SendBatchItemResult{result}) {
		t.Fatalf("response results = %#v, want %#v", resp.Results, []message.SendBatchItemResult{result})
	}
	if !reflect.DeepEqual(submitter.target, target) {
		t.Fatalf("target = %#v, want %#v", submitter.target, target)
	}
	if len(submitter.items) != 1 {
		t.Fatalf("items len = %d, want 1", len(submitter.items))
	}
	if !reflect.DeepEqual(submitter.items[0].Command, cmd) {
		t.Fatalf("command = %#v, want %#v", submitter.items[0].Command, cmd)
	}
	if submitter.items[0].Context == nil {
		t.Fatal("item context = nil, want handler context")
	}
	if submitter.items[0].Deadline.Before(before.Add(900 * time.Millisecond)) {
		t.Fatalf("deadline = %s, want derived from timeout", submitter.items[0].Deadline)
	}
}

func TestSenderAuthorityRPCHandlerRejectsNilSenderAuthority(t *testing.T) {
	body, err := encodeSenderAuthorityRequest(senderAuthorityRequest{
		Target: authority.Target{LeaderNodeID: 3},
		Items:  []senderAuthorityItem{{Command: senderAuthorityTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}

	respBody, err := New(Options{}).HandleSenderAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleSenderAuthorityRPC() error = %v", err)
	}
	resp, err := decodeSenderAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusRejected)
	}
}

func TestSenderAuthorityClientCallsExpectedServiceAndDecodesResults(t *testing.T) {
	target := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 42, RouteRevision: 4, AuthorityEpoch: 5}
	cmd := senderAuthorityTestCommand()
	deadline := time.Now().Add(2 * time.Second)
	result := message.SendBatchItemResult{Result: message.SendResult{MessageID: 1001, MessageSeq: 10, Reason: message.ReasonSuccess}}
	node := &fakeSenderAuthorityRPCNode{response: senderAuthorityResponse{Status: rpcStatusOK, Results: []message.SendBatchItemResult{result}}}
	client := NewClient(node)

	got := client.SendBatchToAuthority(context.Background(), target, []message.SendBatchItem{{
		Context:  context.Background(),
		Deadline: deadline,
		Command:  cmd,
	}})

	if node.nodeID != target.LeaderNodeID {
		t.Fatalf("nodeID = %d, want %d", node.nodeID, target.LeaderNodeID)
	}
	if node.serviceID != SenderAuthorityRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, SenderAuthorityRPCServiceID)
	}
	req, err := decodeSenderAuthorityRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityRequest(client payload) error = %v", err)
	}
	if !reflect.DeepEqual(req.Target, target) {
		t.Fatalf("target = %#v, want %#v", req.Target, target)
	}
	if len(req.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(req.Items))
	}
	if !reflect.DeepEqual(req.Items[0].Command, cmd) {
		t.Fatalf("command = %#v, want %#v", req.Items[0].Command, cmd)
	}
	if req.Items[0].Timeout <= 0 || req.Items[0].Timeout > 2*time.Second {
		t.Fatalf("timeout = %s, want relative timeout capped by item deadline", req.Items[0].Timeout)
	}
	if !reflect.DeepEqual(got, []message.SendBatchItemResult{result}) {
		t.Fatalf("SendBatchToAuthority() = %#v, want %#v", got, []message.SendBatchItemResult{result})
	}
}

func TestSenderAuthorityClientMapsErrorsToItemAlignedResults(t *testing.T) {
	items := []message.SendBatchItem{
		{Command: senderAuthorityTestCommand()},
		{Command: senderAuthorityTestCommand()},
	}

	tests := []struct {
		name       string
		node       *fakeSenderAuthorityRPCNode
		wantString string
		wantIs     error
	}{
		{
			name:       "rejected status",
			node:       &fakeSenderAuthorityRPCNode{response: senderAuthorityResponse{Status: rpcStatusRejected}},
			wantString: "internalv2/access/node: sender authority rpc rejected",
		},
		{
			name:       "transport error",
			node:       &fakeSenderAuthorityRPCNode{err: errors.New("transport down")},
			wantString: "transport down",
		},
		{
			name:   "short response",
			node:   &fakeSenderAuthorityRPCNode{response: senderAuthorityResponse{Status: rpcStatusOK, Results: []message.SendBatchItemResult{{}}}},
			wantIs: message.ErrAppendResultMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClient(tt.node).SendBatchToAuthority(context.Background(), authority.Target{LeaderNodeID: 9}, items)
			if len(got) != len(items) {
				t.Fatalf("results len = %d, want %d", len(got), len(items))
			}
			for i := range got {
				if got[i].Err == nil {
					t.Fatalf("result[%d].Err = nil, want error", i)
				}
				if tt.wantIs != nil {
					if !errors.Is(got[i].Err, tt.wantIs) {
						t.Fatalf("result[%d].Err = %v, want %v", i, got[i].Err, tt.wantIs)
					}
					continue
				}
				if got[i].Err.Error() != tt.wantString {
					t.Fatalf("result[%d].Err = %v, want %q", i, got[i].Err, tt.wantString)
				}
			}
		})
	}
}

type fakeSenderAuthoritySubmitter struct {
	target  authority.Target
	items   []message.SendBatchItem
	results []message.SendBatchItemResult
}

func (f *fakeSenderAuthoritySubmitter) SendBatchForAuthority(_ context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	f.target = target
	f.items = append([]message.SendBatchItem(nil), items...)
	return f.results
}

type fakeSenderAuthorityRPCNode struct {
	response  senderAuthorityResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeSenderAuthorityRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	return encodeSenderAuthorityResponse(f.response)
}
