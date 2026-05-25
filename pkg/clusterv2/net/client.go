package clusternet

import "context"

// Caller sends one typed RPC to a peer node.
type Caller interface {
	// Call invokes serviceID on nodeID with payload.
	Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error)
}
