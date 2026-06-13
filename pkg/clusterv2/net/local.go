package clusternet

import (
	"context"
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
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

// CallOwned invokes serviceID on nodeID and releases payload after local dispatch.
func (n *LocalNetwork) CallOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) ([]byte, error) {
	defer payload.Release()
	return n.Call(ctx, nodeID, serviceID, payload.Bytes())
}

// CallShardOwned invokes serviceID on nodeID with caller-owned bytes; shardKey is ignored locally.
func (n *LocalNetwork) CallShardOwned(ctx context.Context, nodeID uint64, serviceID uint8, _ uint64, payload transportv2.OwnedBuffer) ([]byte, error) {
	return n.CallOwned(ctx, nodeID, serviceID, payload)
}

// Send invokes serviceID on nodeID without returning the handler response.
func (n *LocalNetwork) Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	_, err := n.Call(ctx, nodeID, serviceID, payload)
	return err
}

// SendOwned invokes serviceID on nodeID without returning the handler response.
func (n *LocalNetwork) SendOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) error {
	_, err := n.CallOwned(ctx, nodeID, serviceID, payload)
	return err
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
