package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

func TestDeliveryPusherUsesLocalOwner(t *testing.T) {
	cmd := testInfraDeliveryPushCommand(1)
	local := &fakeRuntimeDeliveryPusher{result: runtimedelivery.PushResult{Accepted: cmd.Routes}}
	remoteNode := &fakeDeliveryClusterNode{}
	pusher := NewDeliveryPusher(1, local, accessnode.NewClient(remoteNode))

	result, err := pusher.Push(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(local.commands) != 1 || !reflect.DeepEqual(local.commands[0], cmd) {
		t.Fatalf("local commands = %#v, want %#v", local.commands, cmd)
	}
	if len(remoteNode.calls) != 0 {
		t.Fatalf("remote calls = %d, want 0", len(remoteNode.calls))
	}
	if !reflect.DeepEqual(result.Accepted, cmd.Routes) {
		t.Fatalf("accepted routes = %#v, want %#v", result.Accepted, cmd.Routes)
	}
}

func TestDeliveryPusherDropsLocalOwnerWhenLocalPusherMissing(t *testing.T) {
	cmd := testInfraDeliveryPushCommand(1)
	pusher := NewDeliveryPusher(1, nil, nil)

	result, err := pusher.Push(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !reflect.DeepEqual(result.Dropped, cmd.Routes) {
		t.Fatalf("dropped routes = %#v, want %#v", result.Dropped, cmd.Routes)
	}
	if len(result.Accepted) != 0 {
		t.Fatalf("accepted routes = %#v, want empty", result.Accepted)
	}
	if len(result.Retryable) != 0 {
		t.Fatalf("retryable routes = %#v, want empty", result.Retryable)
	}
}

func TestDeliveryPusherRoutesRemoteOwnerThroughRPC(t *testing.T) {
	cmd := testInfraDeliveryPushCommand(2)
	remoteOwner := &fakeRuntimeDeliveryPusher{result: runtimedelivery.PushResult{Accepted: []runtimedelivery.Route{cmd.Routes[0]}}}
	remoteNode := &fakeDeliveryClusterNode{
		handler: deliveryRPCHandler{adapter: accessnode.New(accessnode.Options{Delivery: remoteOwner})},
	}
	pusher := NewDeliveryPusher(1, nil, accessnode.NewClient(remoteNode))

	result, err := pusher.Push(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(remoteOwner.commands) != 1 || !reflect.DeepEqual(remoteOwner.commands[0], cmd) {
		t.Fatalf("remote commands = %#v, want %#v", remoteOwner.commands, cmd)
	}
	if len(remoteNode.calls) != 1 {
		t.Fatalf("remote calls = %d, want 1", len(remoteNode.calls))
	}
	if remoteNode.calls[0].nodeID != 2 || remoteNode.calls[0].serviceID != accessnode.DeliveryPushRPCServiceID {
		t.Fatalf("remote call = %#v", remoteNode.calls[0])
	}
	if !reflect.DeepEqual(result.Accepted, []runtimedelivery.Route{cmd.Routes[0]}) {
		t.Fatalf("accepted routes = %#v", result.Accepted)
	}
}

func TestDeliveryPusherMarksRemoteFailureRetryable(t *testing.T) {
	cmd := testInfraDeliveryPushCommand(2)

	missingRemote := NewDeliveryPusher(1, nil, nil)
	result, err := missingRemote.Push(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Push(missing remote) error = %v", err)
	}
	if !reflect.DeepEqual(result.Retryable, cmd.Routes) {
		t.Fatalf("missing remote retryable = %#v, want %#v", result.Retryable, cmd.Routes)
	}

	remoteError := NewDeliveryPusher(1, nil, accessnode.NewClient(&fakeDeliveryClusterNode{err: errInfraDeliveryRPC}))
	result, err = remoteError.Push(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Push(remote error) error = %v", err)
	}
	if !reflect.DeepEqual(result.Retryable, cmd.Routes) {
		t.Fatalf("remote error retryable = %#v, want %#v", result.Retryable, cmd.Routes)
	}
}

func testInfraDeliveryPushCommand(ownerNodeID uint64) runtimedelivery.PushCommand {
	return runtimedelivery.PushCommand{
		OwnerNodeID: ownerNodeID,
		Envelope: runtimedelivery.Envelope{
			MessageID:       1001,
			MessageSeq:      7,
			ChannelID:       "channel-1",
			ChannelType:     2,
			FromUID:         "sender",
			SenderNodeID:    9,
			SenderSessionID: 99,
			ClientMsgNo:     "client-1",
			RedDot:          true,
			Payload:         []byte{1, 2, 3},
		},
		Routes: []runtimedelivery.Route{{
			UID:         "u1",
			OwnerNodeID: ownerNodeID,
			OwnerBootID: 11,
			OwnerSeq:    12,
			SessionID:   101,
			DeviceID:    "device-1",
			DeviceFlag:  1,
			DeviceLevel: 2,
		}},
	}
}

type fakeRuntimeDeliveryPusher struct {
	result   runtimedelivery.PushResult
	err      error
	commands []runtimedelivery.PushCommand
}

func (f *fakeRuntimeDeliveryPusher) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	f.commands = append(f.commands, cmd)
	if f.err != nil {
		return runtimedelivery.PushResult{}, f.err
	}
	return f.result, nil
}

type deliveryRPCHandler struct {
	adapter *accessnode.Adapter
}

func (h deliveryRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return h.adapter.HandleDeliveryPushRPC(ctx, payload)
}

type fakeDeliveryClusterNode struct {
	handler deliveryRPCHandler
	err     error
	calls   []deliveryClusterCall
}

func (f *fakeDeliveryClusterNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, deliveryClusterCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if f.err != nil {
		return nil, f.err
	}
	return f.handler.HandleRPC(ctx, payload)
}

type deliveryClusterCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

var errInfraDeliveryRPC = errors.New("delivery rpc")
