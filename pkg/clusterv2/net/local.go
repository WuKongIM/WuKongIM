package clusternet

import (
	"context"
	"fmt"
	"sync"
)

// LocalNetwork is an in-memory typed RPC network for tests.
type LocalNetwork struct {
	mu       sync.RWMutex
	handlers map[uint64]map[uint8]Handler
}

// NewLocalNetwork creates an empty LocalNetwork.
func NewLocalNetwork() *LocalNetwork {
	return &LocalNetwork{handlers: make(map[uint64]map[uint8]Handler)}
}

// Register registers handler for serviceID on nodeID.
func (n *LocalNetwork) Register(nodeID uint64, serviceID uint8, handler Handler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	services := n.handlers[nodeID]
	if services == nil {
		services = make(map[uint8]Handler)
		n.handlers[nodeID] = services
	}
	services[serviceID] = handler
}

// Call invokes serviceID on nodeID.
func (n *LocalNetwork) Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	n.mu.RLock()
	services, ok := n.handlers[nodeID]
	if !ok {
		n.mu.RUnlock()
		return nil, fmt.Errorf("%w: node %d", ErrNodeNotFound, nodeID)
	}
	handler, ok := services[serviceID]
	n.mu.RUnlock()
	if !ok || handler == nil {
		return nil, fmt.Errorf("%w: node %d service %d", ErrServiceNotFound, nodeID, serviceID)
	}
	return handler.HandleRPC(ctx, append([]byte(nil), payload...))
}

// Send invokes serviceID on nodeID without returning the handler response.
func (n *LocalNetwork) Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	_, err := n.Call(ctx, nodeID, serviceID, payload)
	return err
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
