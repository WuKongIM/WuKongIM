package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

func TestChannelAppendRPCHandlerSubmitsToLocalAuthority(t *testing.T) {
	target := channelAppendTestTarget()
	cmd := channelAppendTestCommand()
	result := channelappend.SendBatchItemResult{Result: channelappend.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelappend.ReasonSuccess}}
	local := &fakeChannelAppendSubmitter{results: []channelappend.SendBatchItemResult{result}}
	adapter := NewChannelAppendAdapter(ChannelAppendOptions{ChannelAppend: local})
	body, err := encodeChannelAppendRequest(channelAppendRequest{
		Target: target,
		Items:  []channelAppendItem{{Command: cmd, Timeout: time.Second}},
	})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}
	before := time.Now()

	respBody, err := adapter.HandleChannelAppendRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelAppendRPC() error = %v", err)
	}
	resp, err := decodeChannelAppendResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelAppendResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if !reflect.DeepEqual(resp.Results, []channelappend.SendBatchItemResult{result}) {
		t.Fatalf("response results = %#v, want %#v", resp.Results, []channelappend.SendBatchItemResult{result})
	}
	if !reflect.DeepEqual(local.target, target) {
		t.Fatalf("target = %#v, want %#v", local.target, target)
	}
	if len(local.items) != 1 {
		t.Fatalf("items len = %d, want 1", len(local.items))
	}
	if !reflect.DeepEqual(local.items[0].Command, cmd) {
		t.Fatalf("command = %#v, want %#v", local.items[0].Command, cmd)
	}
	if local.items[0].Context == nil {
		t.Fatal("item context = nil, want handler context")
	}
	if local.items[0].Deadline.Before(before.Add(900 * time.Millisecond)) {
		t.Fatalf("deadline = %s, want derived from timeout", local.items[0].Deadline)
	}
}

func TestChannelAppendRPCHandlerReturnsItemAlignedResults(t *testing.T) {
	target := channelAppendTestTarget()
	results := []channelappend.SendBatchItemResult{
		{Result: channelappend.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelappend.ReasonSuccess}},
		{Err: channelappend.ErrChannelBusy},
	}
	local := &fakeChannelAppendSubmitter{results: results}
	adapter := NewChannelAppendAdapter(ChannelAppendOptions{ChannelAppend: local})
	body, err := encodeChannelAppendRequest(channelAppendRequest{
		Target: target,
		Items: []channelAppendItem{
			{Command: channelAppendTestCommand()},
			{Command: channelAppendTestCommand()},
		},
	})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}

	respBody, err := adapter.HandleChannelAppendRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelAppendRPC() error = %v", err)
	}
	resp, err := decodeChannelAppendResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelAppendResponse() error = %v", err)
	}
	if len(resp.Results) != len(results) {
		t.Fatalf("response results len = %d, want %d", len(resp.Results), len(results))
	}
	if !errors.Is(resp.Results[1].Err, channelappend.ErrChannelBusy) {
		t.Fatalf("result[1].Err = %v, want ErrChannelBusy", resp.Results[1].Err)
	}
}

func TestChannelAppendRPCHandlerRejectsNilChannelAppend(t *testing.T) {
	body, err := encodeChannelAppendRequest(channelAppendRequest{
		Target: channelAppendTestTarget(),
		Items:  []channelAppendItem{{Command: channelAppendTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}

	respBody, err := NewChannelAppendAdapter(ChannelAppendOptions{}).HandleChannelAppendRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelAppendRPC() error = %v", err)
	}
	resp, err := decodeChannelAppendResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelAppendResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusRejected)
	}
}

func TestChannelAppendClientCallsExpectedServiceAndLeader(t *testing.T) {
	target := channelAppendTestTarget()
	target.LeaderNodeID = 42
	cmd := channelAppendTestCommand()
	result := channelappend.SendBatchItemResult{Result: channelappend.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelappend.ReasonSuccess}}
	node := &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{result}}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), target, []channelappend.SendBatchItem{{
		Context:  context.Background(),
		Deadline: time.Now().Add(2 * time.Second),
		Command:  cmd,
	}})

	if node.nodeID != target.LeaderNodeID {
		t.Fatalf("nodeID = %d, want %d", node.nodeID, target.LeaderNodeID)
	}
	if node.serviceID != ChannelAppendRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, ChannelAppendRPCServiceID)
	}
	req, err := decodeChannelAppendRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeChannelAppendRequest(client payload) error = %v", err)
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
	if !reflect.DeepEqual(got, []channelappend.SendBatchItemResult{result}) {
		t.Fatalf("ForwardSendBatch() = %#v, want %#v", got, []channelappend.SendBatchItemResult{result})
	}
}

func TestChannelAppendClientMapsStatusesAndErrorsToItemAlignedResults(t *testing.T) {
	items := []channelappend.SendBatchItem{
		{Command: channelAppendTestCommand()},
		{Command: channelAppendTestCommand()},
	}

	tests := []struct {
		name       string
		node       *fakeChannelAppendRPCNode
		wantIs     error
		wantString string
	}{
		{name: "not leader status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusNotLeader}}, wantIs: channelappend.ErrNotLeader},
		{name: "not channel authority status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: "not_channel_authority"}}, wantIs: channelappend.ErrNotChannelAuthority},
		{name: "stale route status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusStaleRoute}}, wantIs: channelappend.ErrStaleRoute},
		{name: "route not ready status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusRouteNotReady}}, wantIs: channelappend.ErrRouteNotReady},
		{name: "backpressured status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: "backpressured"}}, wantIs: channelappend.ErrBackpressured},
		{name: "append result missing status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: "append_result_missing"}}, wantIs: channelappend.ErrAppendResultMissing},
		{name: "context canceled status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusContextCanceled}}, wantIs: context.Canceled},
		{name: "deadline status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusContextDeadlineExceeded}}, wantIs: context.DeadlineExceeded},
		{name: "rejected status", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusRejected}}, wantString: "internalv2/access/node: channel append rpc rejected"},
		{name: "transport canceled", node: &fakeChannelAppendRPCNode{err: transportv2.ErrCanceled}, wantIs: context.Canceled},
		{name: "transport timeout", node: &fakeChannelAppendRPCNode{err: transportv2.ErrTimeout}, wantIs: context.DeadlineExceeded},
		{name: "transport error", node: &fakeChannelAppendRPCNode{err: errors.New("transport down")}, wantString: "transport down"},
		{name: "short response", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{{}}}}, wantIs: channelappend.ErrAppendResultMissing},
		{name: "item error", node: &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{{Err: channelappend.ErrChannelBusy}, {Err: channelappend.ErrChannelBusy}}}}, wantIs: channelappend.ErrChannelBusy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClient(tt.node).ForwardSendBatch(context.Background(), channelAppendTestTarget(), items)
			if len(got) != len(items) {
				t.Fatalf("results len = %d, want %d", len(got), len(items))
			}
			for i := range got {
				if got[i].Err == nil {
					t.Fatalf("result[%d].Err = nil, want error", i)
				}
				if tt.wantString != "" {
					if got[i].Err.Error() != tt.wantString {
						t.Fatalf("result[%d].Err = %v, want %q", i, got[i].Err, tt.wantString)
					}
					continue
				}
				if !errors.Is(got[i].Err, tt.wantIs) {
					t.Fatalf("result[%d].Err = %v, want %v", i, got[i].Err, tt.wantIs)
				}
			}
		})
	}
}

func TestChannelAppendClientSkipsExpiredCanceledItemsAndPreservesActiveOrder(t *testing.T) {
	expired := channelAppendTestCommand()
	expired.ClientMsgNo = "expired"
	canceled := channelAppendTestCommand()
	canceled.ClientMsgNo = "canceled"
	firstActive := channelAppendTestCommand()
	firstActive.ClientMsgNo = "active-1"
	secondActive := channelAppendTestCommand()
	secondActive.ClientMsgNo = "active-2"
	firstResult := channelappend.SendBatchItemResult{Result: channelappend.SendResult{MessageID: 1002, MessageSeq: 12, Reason: channelappend.ReasonSuccess}}
	secondResult := channelappend.SendBatchItemResult{Result: channelappend.SendResult{MessageID: 1003, MessageSeq: 13, Reason: channelappend.ReasonSuccess}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{firstResult, secondResult}}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), channelAppendTestTarget(), []channelappend.SendBatchItem{
		{Deadline: time.Now().Add(-time.Second), Command: expired},
		{Command: firstActive},
		{Context: ctx, Command: canceled},
		{Command: secondActive},
	})

	if !node.called {
		t.Fatal("CallRPC was not called for active items")
	}
	req, err := decodeChannelAppendRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeChannelAppendRequest(client payload) error = %v", err)
	}
	if len(req.Items) != 2 {
		t.Fatalf("rpc item len = %d, want 2", len(req.Items))
	}
	if req.Items[0].Command.ClientMsgNo != "active-1" || req.Items[1].Command.ClientMsgNo != "active-2" {
		t.Fatalf("active order = %q/%q, want active-1/active-2", req.Items[0].Command.ClientMsgNo, req.Items[1].Command.ClientMsgNo)
	}
	if len(got) != 4 {
		t.Fatalf("results len = %d, want 4", len(got))
	}
	if !errors.Is(got[0].Err, context.DeadlineExceeded) {
		t.Fatalf("result[0].Err = %v, want context.DeadlineExceeded", got[0].Err)
	}
	if got[1] != firstResult {
		t.Fatalf("result[1] = %#v, want %#v", got[1], firstResult)
	}
	if !errors.Is(got[2].Err, context.Canceled) {
		t.Fatalf("result[2].Err = %v, want context.Canceled", got[2].Err)
	}
	if got[3] != secondResult {
		t.Fatalf("result[3] = %#v, want %#v", got[3], secondResult)
	}
}

func TestChannelAppendClientSkipsAllInactiveItemsWithoutRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := &fakeChannelAppendRPCNode{response: channelAppendResponse{Status: rpcStatusOK}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), channelAppendTestTarget(), []channelappend.SendBatchItem{
		{Deadline: time.Now().Add(-time.Second), Command: channelAppendTestCommand()},
		{Context: ctx, Command: channelAppendTestCommand()},
	})

	if node.called {
		t.Fatal("CallRPC was called for inactive items")
	}
	if len(got) != 2 {
		t.Fatalf("results len = %d, want 2", len(got))
	}
	if !errors.Is(got[0].Err, context.DeadlineExceeded) {
		t.Fatalf("result[0].Err = %v, want context.DeadlineExceeded", got[0].Err)
	}
	if !errors.Is(got[1].Err, context.Canceled) {
		t.Fatalf("result[1].Err = %v, want context.Canceled", got[1].Err)
	}
}

type fakeChannelAppendSubmitter struct {
	target  channelappend.AuthorityTarget
	items   []channelappend.SendBatchItem
	results []channelappend.SendBatchItemResult
}

func (f *fakeChannelAppendSubmitter) SubmitForAuthority(_ context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	f.target = target
	f.items = append([]channelappend.SendBatchItem(nil), items...)
	return f.results
}

type fakeChannelAppendRPCNode struct {
	response  channelAppendResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
	called    bool
}

func (f *fakeChannelAppendRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.called = true
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	return encodeChannelAppendResponse(f.response)
}
