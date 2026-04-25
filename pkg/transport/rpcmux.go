package transport

import (
	"context"
	"fmt"
	"sync"
)

type RPCMux struct {
	mu       sync.RWMutex
	handlers map[uint8]RPCHandler
}

func NewRPCMux() *RPCMux {
	return &RPCMux{
		handlers: make(map[uint8]RPCHandler),
	}
}

func (m *RPCMux) Handle(serviceID uint8, handler RPCHandler) {
	if serviceID == 0 {
		panic("nodetransport: reserved rpc service id")
	}
	m.mu.Lock()
	if _, exists := m.handlers[serviceID]; exists {
		m.mu.Unlock()
		panic(fmt.Sprintf("nodetransport: duplicate rpc service id %d", serviceID))
	}
	m.handlers[serviceID] = handler
	m.mu.Unlock()
}

func (m *RPCMux) Unhandle(serviceID uint8) {
	if serviceID == 0 {
		return
	}
	m.mu.Lock()
	delete(m.handlers, serviceID)
	m.mu.Unlock()
}

func (m *RPCMux) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	serviceID, body, err := decodeRPCServicePayload(payload)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	handler := m.handlers[serviceID]
	m.mu.RUnlock()
	if handler == nil {
		return nil, fmt.Errorf("nodetransport: unknown rpc service %d", serviceID)
	}
	return handler(ctx, body)
}

func encodeRPCServicePayload(serviceID uint8, payload []byte) []byte {
	body := make([]byte, 1+len(payload))
	body[0] = serviceID
	copy(body[1:], payload)
	return body
}

func decodeRPCServicePayload(payload []byte) (uint8, []byte, error) {
	if len(payload) == 0 {
		return 0, nil, fmt.Errorf("nodetransport: rpc payload missing service id")
	}
	return payload[0], payload[1:], nil
}
