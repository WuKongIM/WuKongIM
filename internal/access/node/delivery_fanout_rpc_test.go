package node

import (
	"context"
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func TestDeliveryFanoutRPCHandlerDispatchesTask(t *testing.T) {
	task := testDeliveryFanoutTask()
	runner := &fakeDeliveryFanoutRunner{}
	body, err := encodeDeliveryFanoutRequest(deliveryFanoutRequest{Task: task})
	if err != nil {
		t.Fatalf("encodeDeliveryFanoutRequest() error = %v", err)
	}

	respBody, err := New(Options{DeliveryFanout: runner}).HandleDeliveryFanoutRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleDeliveryFanoutRPC() error = %v", err)
	}
	resp, err := decodeDeliveryFanoutResponse(respBody)
	if err != nil {
		t.Fatalf("decodeDeliveryFanoutResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("status = %q, want ok", resp.Status)
	}
	if !reflect.DeepEqual(runner.tasks, []runtimedelivery.FanoutTask{task}) {
		t.Fatalf("tasks = %#v, want %#v", runner.tasks, []runtimedelivery.FanoutTask{task})
	}
}

func TestDeliveryFanoutRPCClientForwardsTask(t *testing.T) {
	task := testDeliveryFanoutTask()
	remote := &fakeDeliveryFanoutRunner{}
	node := &fakeDeliveryFanoutRPCNode{
		handler: deliveryFanoutRPCHandler{adapter: New(Options{DeliveryFanout: remote})},
	}
	client := NewClient(node)

	err := client.ForwardFanoutTask(context.Background(), 13, task)
	if err != nil {
		t.Fatalf("ForwardFanoutTask() error = %v", err)
	}

	if len(node.calls) != 1 || node.calls[0].nodeID != 13 || node.calls[0].serviceID != DeliveryFanoutRPCServiceID {
		t.Fatalf("remote calls = %#v, want one fanout call to node 13", node.calls)
	}
	if !reflect.DeepEqual(remote.tasks, []runtimedelivery.FanoutTask{task}) {
		t.Fatalf("remote tasks = %#v, want %#v", remote.tasks, []runtimedelivery.FanoutTask{task})
	}
}

func TestDeliveryFanoutRPCClientReturnsRejectedError(t *testing.T) {
	node := &fakeDeliveryFanoutRPCNode{
		response: deliveryFanoutResponse{Status: rpcStatusRejected},
	}
	client := NewClient(node)

	if err := client.ForwardFanoutTask(context.Background(), 13, testDeliveryFanoutTask()); err == nil {
		t.Fatal("ForwardFanoutTask() error = nil, want rejected error")
	}
}

func testDeliveryFanoutTask() runtimedelivery.FanoutTask {
	return runtimedelivery.FanoutTask{
		Envelope: runtimedelivery.Envelope{
			MessageID:         1001,
			MessageSeq:        7,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "sender",
			SenderNodeID:      9,
			SenderSessionID:   99,
			ClientMsgNo:       "client-1",
			RedDot:            true,
			Payload:           []byte("payload"),
			MessageScopedUIDs: []string{"u1"},
		},
		Partition: runtimedelivery.Partition{ID: 3, LeaderNodeID: 13, HashSlotStart: 8, HashSlotEnd: 15},
		Cursor:    "cursor-1",
		Attempt:   2,
	}
}

type fakeDeliveryFanoutRunner struct {
	tasks []runtimedelivery.FanoutTask
	err   error
}

func (f *fakeDeliveryFanoutRunner) RunTask(_ context.Context, task runtimedelivery.FanoutTask) error {
	f.tasks = append(f.tasks, task)
	return f.err
}

type deliveryFanoutRPCHandler struct {
	adapter *Adapter
}

func (h deliveryFanoutRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return h.adapter.HandleDeliveryFanoutRPC(ctx, payload)
}

type fakeDeliveryFanoutRPCNode struct {
	handler  deliveryFanoutRPCHandler
	response deliveryFanoutResponse
	err      error
	calls    []deliveryFanoutRPCCall
}

func (f *fakeDeliveryFanoutRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, deliveryFanoutRPCCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if f.err != nil {
		return nil, f.err
	}
	if f.response.Status != "" {
		return encodeDeliveryFanoutResponse(f.response)
	}
	return f.handler.HandleRPC(ctx, payload)
}

type deliveryFanoutRPCCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}
