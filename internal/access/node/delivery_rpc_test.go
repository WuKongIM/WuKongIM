package node

import (
	"context"
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func TestDeliveryRPCHandlerDispatchesPush(t *testing.T) {
	cmd := testDeliveryPushCommand()
	result := runtimedelivery.PushResult{Accepted: []runtimedelivery.Route{cmd.Routes[0]}}
	delivery := &fakeDeliveryOwnerPush{result: result}
	adapter := New(Options{Delivery: delivery})
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: cmd})
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}

	respBody, err := adapter.HandleDeliveryPushRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleDeliveryPushRPC() error = %v", err)
	}
	resp, err := decodeDeliveryPushResponse(respBody)
	if err != nil {
		t.Fatalf("decodeDeliveryPushResponse() error = %v", err)
	}
	if resp.Status != rpcStatusOK {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusOK)
	}
	if len(delivery.commands) != 1 || !reflect.DeepEqual(delivery.commands[0], cmd) {
		t.Fatalf("delivery commands = %#v, want %#v", delivery.commands, cmd)
	}
	if !reflect.DeepEqual(resp.Result, result) {
		t.Fatalf("response result = %#v, want %#v", resp.Result, result)
	}
}

func TestDeliveryRPCHandlerRejectsNilDelivery(t *testing.T) {
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: testDeliveryPushCommand()})
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}

	respBody, err := New(Options{}).HandleDeliveryPushRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleDeliveryPushRPC() error = %v", err)
	}
	resp, err := decodeDeliveryPushResponse(respBody)
	if err != nil {
		t.Fatalf("decodeDeliveryPushResponse() error = %v", err)
	}
	if resp.Status != rpcStatusRejected {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusRejected)
	}
}

func TestDeliveryClientCallsExpectedServiceAndDecodesResult(t *testing.T) {
	cmd := testDeliveryPushCommand()
	result := runtimedelivery.PushResult{Accepted: []runtimedelivery.Route{cmd.Routes[0]}}
	node := &fakeDeliveryRPCNode{response: deliveryPushResponse{Status: rpcStatusOK, Result: result}}
	client := NewClient(node)

	got, err := client.PushBatch(context.Background(), 13, cmd)
	if err != nil {
		t.Fatalf("PushBatch() error = %v", err)
	}
	if node.nodeID != 13 {
		t.Fatalf("nodeID = %d, want 13", node.nodeID)
	}
	if node.serviceID != DeliveryPushRPCServiceID {
		t.Fatalf("serviceID = %d, want %d", node.serviceID, DeliveryPushRPCServiceID)
	}
	req, err := decodeDeliveryPushRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeDeliveryPushRequest(client payload) error = %v", err)
	}
	if !reflect.DeepEqual(req.Command, cmd) {
		t.Fatalf("client command = %#v, want %#v", req.Command, cmd)
	}
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("PushBatch() = %#v, want %#v", got, result)
	}
}

func TestDeliveryClientMapsRejectedStatusToError(t *testing.T) {
	client := NewClient(&fakeDeliveryRPCNode{
		response: deliveryPushResponse{Status: rpcStatusRejected},
	})

	if _, err := client.PushBatch(context.Background(), 13, testDeliveryPushCommand()); err == nil {
		t.Fatal("PushBatch() error = nil, want rejected error")
	}
}

type fakeDeliveryOwnerPush struct {
	result   runtimedelivery.PushResult
	err      error
	commands []runtimedelivery.PushCommand
}

func (f *fakeDeliveryOwnerPush) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	f.commands = append(f.commands, cmd)
	if f.err != nil {
		return runtimedelivery.PushResult{}, f.err
	}
	return f.result, nil
}

type fakeDeliveryRPCNode struct {
	response  deliveryPushResponse
	err       error
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeDeliveryRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	if f.err != nil {
		return nil, f.err
	}
	return encodeDeliveryPushResponse(f.response)
}
