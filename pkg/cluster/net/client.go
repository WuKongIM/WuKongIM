package clusternet

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

// Caller sends one typed RPC to a peer node.
type Caller interface {
	// Call invokes serviceID on nodeID with payload.
	Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error)
}

// OwnedCaller sends one typed RPC with caller-owned payload bytes.
type OwnedCaller interface {
	// CallOwned invokes serviceID on nodeID and transfers payload ownership.
	CallOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) ([]byte, error)
}

// ShardCaller sends one typed RPC to a caller-selected peer connection shard.
type ShardCaller interface {
	// CallShard invokes serviceID on nodeID with payload using shardKey for connection selection.
	CallShard(ctx context.Context, nodeID uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error)
}

// OwnedShardCaller sends one typed RPC with caller-owned bytes to a selected peer shard.
type OwnedShardCaller interface {
	// CallShardOwned invokes serviceID on nodeID with shardKey and transfers payload ownership.
	CallShardOwned(ctx context.Context, nodeID uint64, serviceID uint8, shardKey uint64, payload transportv2.OwnedBuffer) ([]byte, error)
}

// Sender sends one typed message to a peer node without waiting for a response.
type Sender interface {
	// Send enqueues payload for serviceID on nodeID.
	Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error
}

// OwnedSender sends one typed message with caller-owned payload bytes.
type OwnedSender interface {
	// SendOwned enqueues payload for serviceID on nodeID and transfers payload ownership.
	SendOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) error
}

// CallOwnedPayload invokes caller with payload bytes that will not be reused by the caller.
func CallOwnedPayload(ctx context.Context, caller Caller, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if owned, ok := caller.(OwnedCaller); ok {
		return owned.CallOwned(ctx, nodeID, serviceID, transportv2.NewOwnedBuffer(payload, nil))
	}
	return caller.Call(ctx, nodeID, serviceID, payload)
}

// CallShardOwnedPayload invokes caller with shardKey and payload bytes that will not be reused by the caller.
func CallShardOwnedPayload(ctx context.Context, caller Caller, nodeID uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error) {
	if owned, ok := caller.(OwnedShardCaller); ok {
		return owned.CallShardOwned(ctx, nodeID, serviceID, shardKey, transportv2.NewOwnedBuffer(payload, nil))
	}
	if sharded, ok := caller.(ShardCaller); ok {
		return sharded.CallShard(ctx, nodeID, serviceID, shardKey, payload)
	}
	return CallOwnedPayload(ctx, caller, nodeID, serviceID, payload)
}

// SendOwnedPayload sends payload bytes that will not be reused by the caller.
func SendOwnedPayload(ctx context.Context, sender Sender, nodeID uint64, serviceID uint8, payload []byte) error {
	if owned, ok := sender.(OwnedSender); ok {
		return owned.SendOwned(ctx, nodeID, serviceID, transportv2.NewOwnedBuffer(payload, nil))
	}
	return sender.Send(ctx, nodeID, serviceID, payload)
}
