package node

import (
	"context"
	"reflect"
	"testing"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
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

func TestDeliveryRPCHandlerAdaptsCanonicalOwnerPush(t *testing.T) {
	cmd := testDeliveryPushCommand()
	canonicalResult := onlinedelivery.OwnerPushResult{
		Accepted: onlineDeliveryRoutesFromLegacy(cmd.Routes[:1]),
	}
	canonical := &fakeOnlineDeliveryOwnerPush{result: canonicalResult}
	adapter := New(Options{Delivery: AdaptOnlineDeliveryOwnerPush(canonical)})
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
	wantPush := onlineDeliveryPushFromLegacy(cmd)
	if len(canonical.pushes) != 1 || !reflect.DeepEqual(canonical.pushes[0], wantPush) {
		t.Fatalf("canonical pushes = %#v, want %#v", canonical.pushes, wantPush)
	}
	wantResult := legacyDeliveryResultFromOnline(canonicalResult)
	if resp.Status != rpcStatusOK || !reflect.DeepEqual(resp.Result, wantResult) {
		t.Fatalf("response = %#v, want status %q and result %#v", resp, rpcStatusOK, wantResult)
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

func TestDeliveryClientPushOwnerUsesStableLegacyWireFormat(t *testing.T) {
	push := onlinedelivery.OwnerPush{
		OwnerNodeID: 13,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2,
			Payload: []byte("payload"), MessageScopedUIDs: []string{"u1"},
		},
		Routes: []onlinedelivery.Route{{
			UID: "u1", OwnerNodeID: 13, OwnerBootID: 7, OwnerSeq: 8,
			SessionID: 9, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 2,
		}},
	}
	legacyResult := runtimedelivery.PushResult{
		Retryable: legacyDeliveryRoutesFromOnline(push.Routes),
	}
	node := &fakeDeliveryRPCNode{
		response: deliveryPushResponse{Status: rpcStatusOK, Result: legacyResult},
	}

	got, err := NewClient(node).PushOwner(context.Background(), push)
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if node.nodeID != push.OwnerNodeID || node.serviceID != DeliveryPushRPCServiceID {
		t.Fatalf("RPC target = node %d service %d, want node %d service %d",
			node.nodeID, node.serviceID, push.OwnerNodeID, DeliveryPushRPCServiceID)
	}
	req, err := decodeDeliveryPushRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeDeliveryPushRequest(client payload) error = %v", err)
	}
	if want := legacyDeliveryPushFromOnline(push); !reflect.DeepEqual(req.Command, want) {
		t.Fatalf("wire command = %#v, want %#v", req.Command, want)
	}
	if want := onlineDeliveryResultFromLegacy(legacyResult); !reflect.DeepEqual(got, want) {
		t.Fatalf("PushOwner() = %#v, want %#v", got, want)
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

type fakeOnlineDeliveryOwnerPush struct {
	result onlinedelivery.OwnerPushResult
	err    error
	pushes []onlinedelivery.OwnerPush
}

func (f *fakeOnlineDeliveryOwnerPush) PushOwner(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	f.pushes = append(f.pushes, push.Clone())
	if f.err != nil {
		return onlinedelivery.OwnerPushResult{}, f.err
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
