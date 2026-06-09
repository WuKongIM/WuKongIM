package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

func TestChannelWriteRPCHandlerSubmitsToLocalAuthority(t *testing.T) {
	target := channelWriteTestTarget()
	cmd := channelWriteTestCommand()
	result := channelwrite.SendBatchItemResult{Result: channelwrite.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelwrite.ReasonSuccess}}
	local := &fakeChannelWriteSubmitter{results: []channelwrite.SendBatchItemResult{result}}
	adapter := NewChannelWriteAdapter(ChannelWriteOptions{ChannelWrite: local})
	body, err := encodeChannelWriteRequest(channelWriteRequest{
		Target: target,
		Items:  []channelWriteItem{{Command: cmd, Timeout: time.Second}},
	})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}
	before := time.Now()

	respBody, err := adapter.HandleChannelWriteRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelWriteRPC() error = %v", err)
	}
	resp, err := decodeChannelWriteResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelWriteResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if !reflect.DeepEqual(resp.Results, []channelwrite.SendBatchItemResult{result}) {
		t.Fatalf("response results = %#v, want %#v", resp.Results, []channelwrite.SendBatchItemResult{result})
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

func TestChannelWriteRPCHandlerReturnsItemAlignedResults(t *testing.T) {
	target := channelWriteTestTarget()
	results := []channelwrite.SendBatchItemResult{
		{Result: channelwrite.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelwrite.ReasonSuccess}},
		{Err: channelwrite.ErrChannelBusy},
	}
	local := &fakeChannelWriteSubmitter{results: results}
	adapter := NewChannelWriteAdapter(ChannelWriteOptions{ChannelWrite: local})
	body, err := encodeChannelWriteRequest(channelWriteRequest{
		Target: target,
		Items: []channelWriteItem{
			{Command: channelWriteTestCommand()},
			{Command: channelWriteTestCommand()},
		},
	})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}

	respBody, err := adapter.HandleChannelWriteRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelWriteRPC() error = %v", err)
	}
	resp, err := decodeChannelWriteResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelWriteResponse() error = %v", err)
	}
	if len(resp.Results) != len(results) {
		t.Fatalf("response results len = %d, want %d", len(resp.Results), len(results))
	}
	if !errors.Is(resp.Results[1].Err, channelwrite.ErrChannelBusy) {
		t.Fatalf("result[1].Err = %v, want ErrChannelBusy", resp.Results[1].Err)
	}
}

func TestChannelWriteRPCHandlerRejectsNilChannelWrite(t *testing.T) {
	body, err := encodeChannelWriteRequest(channelWriteRequest{
		Target: channelWriteTestTarget(),
		Items:  []channelWriteItem{{Command: channelWriteTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}

	respBody, err := NewChannelWriteAdapter(ChannelWriteOptions{}).HandleChannelWriteRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleChannelWriteRPC() error = %v", err)
	}
	resp, err := decodeChannelWriteResponse(respBody)
	if err != nil {
		t.Fatalf("decodeChannelWriteResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusRejected)
	}
}

func TestChannelWriteClientCallsExpectedServiceAndLeader(t *testing.T) {
	target := channelWriteTestTarget()
	target.LeaderNodeID = 42
	cmd := channelWriteTestCommand()
	result := channelwrite.SendBatchItemResult{Result: channelwrite.SendResult{MessageID: 1001, MessageSeq: 10, Reason: channelwrite.ReasonSuccess}}
	node := &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusOK, Results: []channelwrite.SendBatchItemResult{result}}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), target, []channelwrite.SendBatchItem{{
		Context:  context.Background(),
		Deadline: time.Now().Add(2 * time.Second),
		Command:  cmd,
	}})

	if node.nodeID != target.LeaderNodeID {
		t.Fatalf("nodeID = %d, want %d", node.nodeID, target.LeaderNodeID)
	}
	if node.serviceID != ChannelWriteRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, ChannelWriteRPCServiceID)
	}
	req, err := decodeChannelWriteRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeChannelWriteRequest(client payload) error = %v", err)
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
	if !reflect.DeepEqual(got, []channelwrite.SendBatchItemResult{result}) {
		t.Fatalf("ForwardSendBatch() = %#v, want %#v", got, []channelwrite.SendBatchItemResult{result})
	}
}

func TestChannelWriteClientMapsStatusesAndErrorsToItemAlignedResults(t *testing.T) {
	items := []channelwrite.SendBatchItem{
		{Command: channelWriteTestCommand()},
		{Command: channelWriteTestCommand()},
	}

	tests := []struct {
		name       string
		node       *fakeChannelWriteRPCNode
		wantIs     error
		wantString string
	}{
		{name: "not leader status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusNotLeader}}, wantIs: channelwrite.ErrNotLeader},
		{name: "not channel authority status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: "not_channel_authority"}}, wantIs: channelwrite.ErrNotChannelAuthority},
		{name: "stale route status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusStaleRoute}}, wantIs: channelwrite.ErrStaleRoute},
		{name: "route not ready status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusRouteNotReady}}, wantIs: channelwrite.ErrRouteNotReady},
		{name: "backpressured status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: "backpressured"}}, wantIs: channelwrite.ErrBackpressured},
		{name: "append result missing status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: "append_result_missing"}}, wantIs: channelwrite.ErrAppendResultMissing},
		{name: "context canceled status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusContextCanceled}}, wantIs: context.Canceled},
		{name: "deadline status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusContextDeadlineExceeded}}, wantIs: context.DeadlineExceeded},
		{name: "rejected status", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusRejected}}, wantString: "internalv2/access/node: channel write rpc rejected"},
		{name: "transport canceled", node: &fakeChannelWriteRPCNode{err: transportv2.ErrCanceled}, wantIs: context.Canceled},
		{name: "transport timeout", node: &fakeChannelWriteRPCNode{err: transportv2.ErrTimeout}, wantIs: context.DeadlineExceeded},
		{name: "transport error", node: &fakeChannelWriteRPCNode{err: errors.New("transport down")}, wantString: "transport down"},
		{name: "short response", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusOK, Results: []channelwrite.SendBatchItemResult{{}}}}, wantIs: channelwrite.ErrAppendResultMissing},
		{name: "item error", node: &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusOK, Results: []channelwrite.SendBatchItemResult{{Err: channelwrite.ErrChannelBusy}, {Err: channelwrite.ErrChannelBusy}}}}, wantIs: channelwrite.ErrChannelBusy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClient(tt.node).ForwardSendBatch(context.Background(), channelWriteTestTarget(), items)
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

func TestChannelWriteClientSkipsExpiredCanceledItemsAndPreservesActiveOrder(t *testing.T) {
	expired := channelWriteTestCommand()
	expired.ClientMsgNo = "expired"
	canceled := channelWriteTestCommand()
	canceled.ClientMsgNo = "canceled"
	firstActive := channelWriteTestCommand()
	firstActive.ClientMsgNo = "active-1"
	secondActive := channelWriteTestCommand()
	secondActive.ClientMsgNo = "active-2"
	firstResult := channelwrite.SendBatchItemResult{Result: channelwrite.SendResult{MessageID: 1002, MessageSeq: 12, Reason: channelwrite.ReasonSuccess}}
	secondResult := channelwrite.SendBatchItemResult{Result: channelwrite.SendResult{MessageID: 1003, MessageSeq: 13, Reason: channelwrite.ReasonSuccess}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusOK, Results: []channelwrite.SendBatchItemResult{firstResult, secondResult}}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), channelWriteTestTarget(), []channelwrite.SendBatchItem{
		{Deadline: time.Now().Add(-time.Second), Command: expired},
		{Command: firstActive},
		{Context: ctx, Command: canceled},
		{Command: secondActive},
	})

	if !node.called {
		t.Fatal("CallRPC was not called for active items")
	}
	req, err := decodeChannelWriteRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeChannelWriteRequest(client payload) error = %v", err)
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

func TestChannelWriteClientSkipsAllInactiveItemsWithoutRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := &fakeChannelWriteRPCNode{response: channelWriteResponse{Status: rpcStatusOK}}
	client := NewClient(node)

	got := client.ForwardSendBatch(context.Background(), channelWriteTestTarget(), []channelwrite.SendBatchItem{
		{Deadline: time.Now().Add(-time.Second), Command: channelWriteTestCommand()},
		{Context: ctx, Command: channelWriteTestCommand()},
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

type fakeChannelWriteSubmitter struct {
	target  channelwrite.AuthorityTarget
	items   []channelwrite.SendBatchItem
	results []channelwrite.SendBatchItemResult
}

func (f *fakeChannelWriteSubmitter) SubmitForAuthority(_ context.Context, target channelwrite.AuthorityTarget, items []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult {
	f.target = target
	f.items = append([]channelwrite.SendBatchItem(nil), items...)
	return f.results
}

type fakeChannelWriteRPCNode struct {
	response  channelWriteResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
	called    bool
}

func (f *fakeChannelWriteRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.called = true
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	return encodeChannelWriteResponse(f.response)
}
