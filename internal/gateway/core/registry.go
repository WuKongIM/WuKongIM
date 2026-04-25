package core

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

var (
	ErrNilTransportFactory       = errors.New("gateway/core: nil transport factory")
	ErrNilProtocolAdapter        = errors.New("gateway/core: nil protocol adapter")
	ErrTransportFactoryNameEmpty = errors.New("gateway/core: transport factory name is empty")
	ErrProtocolNameEmpty         = errors.New("gateway/core: protocol adapter name is empty")
	ErrDuplicateTransportFactory = errors.New("gateway/core: duplicate transport factory")
	ErrDuplicateProtocolAdapter  = errors.New("gateway/core: duplicate protocol adapter")
	ErrTransportFactoryNotFound  = errors.New("gateway/core: transport factory not found")
	ErrProtocolAdapterNotFound   = errors.New("gateway/core: protocol adapter not found")
)

type Registry struct {
	mu         sync.RWMutex
	transports map[string]transport.Factory
	protocols  map[string]protocol.Adapter
}

func NewRegistry() *Registry {
	return &Registry{
		transports: make(map[string]transport.Factory),
		protocols:  make(map[string]protocol.Adapter),
	}
}

func (r *Registry) RegisterTransport(factory transport.Factory) error {
	if isNil(factory) {
		return ErrNilTransportFactory
	}

	name := factory.Name()
	if name == "" {
		return ErrTransportFactoryNameEmpty
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.transports[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateTransportFactory, name)
	}
	r.transports[name] = factory
	return nil
}

func (r *Registry) Transport(name string) (transport.Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, ok := r.transports[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrTransportFactoryNotFound, name)
	}
	return factory, nil
}

func (r *Registry) RegisterProtocol(adapter protocol.Adapter) error {
	if isNil(adapter) {
		return ErrNilProtocolAdapter
	}

	name := adapter.Name()
	if name == "" {
		return ErrProtocolNameEmpty
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.protocols[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateProtocolAdapter, name)
	}
	r.protocols[name] = adapter
	return nil
}

func (r *Registry) Protocol(name string) (protocol.Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	adapter, ok := r.protocols[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProtocolAdapterNotFound, name)
	}
	return adapter, nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
