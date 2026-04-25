package gnet

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

type listenerHandle struct {
	mu      sync.Mutex
	opts    transport.ListenerOptions
	runtime *listenerRuntime
	group   *engineGroup
	started bool
}

func (h *listenerHandle) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return nil
	}
	if err := h.group.start(h.runtime); err != nil {
		return err
	}
	h.started = true
	return nil
}

func (h *listenerHandle) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.started {
		return nil
	}
	if err := h.group.stop(h.runtime); err != nil {
		return err
	}
	h.started = false
	return nil
}

func (h *listenerHandle) Addr() string {
	if h.runtime != nil {
		if addr := h.runtime.addr(); addr != "" {
			return addr
		}
	}
	return h.opts.Address
}

var _ transport.Listener = (*listenerHandle)(nil)
