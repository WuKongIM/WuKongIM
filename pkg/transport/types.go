package transport

import (
	"context"
	"time"
)

// NodeID identifies a cluster node. Type alias for uint64 — zero-cost
// conversion with multiraft.NodeID (which is a named uint64 type, requiring
// an explicit cast at call sites).
type NodeID = uint64

// MessageHandler processes an inbound message of a specific type.
// The body is valid only for the duration of the callback; copy it if it must
// be retained asynchronously.
type MessageHandler func(body []byte)

// RPCHandler processes an inbound RPC request and returns a response body.
// The ctx passed by the Server is context.Background(). The handler is responsible
// for applying its own timeout.
type RPCHandler func(ctx context.Context, body []byte) ([]byte, error)

// Discovery resolves a NodeID to a network address.
type Discovery interface {
	Resolve(nodeID NodeID) (addr string, err error)
}

type ObserverHooks struct {
	OnSend      func(msgType uint8, bytes int)
	OnReceive   func(msgType uint8, bytes int)
	OnDial      func(event DialEvent)
	OnEnqueue   func(event EnqueueEvent)
	OnRPCClient func(event RPCClientEvent)
}

type PoolPeerStats struct {
	NodeID NodeID
	Active int
	Idle   int
}

// DialEvent reports an outbound dial attempt to a target node.
type DialEvent struct {
	TargetNode NodeID
	Result     string
	Duration   time.Duration
}

// EnqueueEvent reports the outcome of sending work onto a transport path.
type EnqueueEvent struct {
	TargetNode NodeID
	Kind       string
	Result     string
}

// RPCClientEvent reports an outbound RPC lifecycle update.
type RPCClientEvent struct {
	TargetNode NodeID
	ServiceID  uint8
	Result     string
	Duration   time.Duration
	Inflight   int
}
