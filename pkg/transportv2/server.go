package transportv2

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

// Server owns inbound connection and service registry state.
type Server struct {
	// cfg stores normalized inbound transport settings.
	cfg ServerConfig

	// ctx is canceled when Stop begins server shutdown.
	ctx context.Context
	// cancel stops server-owned dispatch response goroutines.
	cancel context.CancelFunc

	// mu protects listener, services, conns, and stopped.
	mu sync.RWMutex
	// listener accepts inbound transport connections after ListenAndServe.
	listener net.Listener
	// services maps service ids to bounded worker pools.
	services map[uint16]*rpc.Service
	// conns tracks active inbound connection actors.
	conns map[*conn.Conn]struct{}
	// stopped rejects new listeners, handlers, and tracked connections.
	stopped bool

	// stopOnce makes Stop idempotent.
	stopOnce sync.Once
}

// NewServer validates config and builds a minimal server shell.
func NewServer(cfg ServerConfig) (*Server, error) {
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:      normalized,
		ctx:      ctx,
		cancel:   cancel,
		services: make(map[uint16]*rpc.Service),
		conns:    make(map[*conn.Conn]struct{}),
	}, nil
}

// Handle registers handler as the bounded worker pool for serviceID.
func (s *Server) Handle(serviceID uint16, handler Handler, opts ServiceOptions) error {
	if handler == nil {
		return fmt.Errorf("%w: service handler is required", ErrInvalidConfig)
	}
	if opts.MaxPayload == 0 {
		opts.MaxPayload = s.cfg.Limits.MaxFrameBodyBytes
	}
	if err := opts.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrStopped
	}
	if _, ok := s.services[serviceID]; ok {
		return fmt.Errorf("%w: duplicate service id %d", ErrInvalidConfig, serviceID)
	}
	s.services[serviceID] = rpc.NewService(serviceID, handler, opts)
	return nil
}

// ListenAndServe starts accepting inbound transport connections on addr.
func (s *Server) ListenAndServe(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		_ = listener.Close()
		return ErrStopped
	}
	if s.listener != nil {
		s.mu.Unlock()
		_ = listener.Close()
		return fmt.Errorf("%w: server already listening", ErrInvalidConfig)
	}
	s.listener = listener
	s.mu.Unlock()

	go s.acceptLoop(listener)
	return nil
}

// Addr returns the listener address, or an empty string before ListenAndServe.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop releases server resources.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()

		s.mu.Lock()
		s.stopped = true
		listener := s.listener
		services := make([]*rpc.Service, 0, len(s.services))
		for _, service := range s.services {
			services = append(services, service)
		}
		conns := make([]*conn.Conn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.conns = make(map[*conn.Conn]struct{})
		s.mu.Unlock()

		if listener != nil {
			_ = listener.Close()
		}
		for _, c := range conns {
			c.Close(core.ErrStopped)
		}
		for _, service := range services {
			service.Stop()
		}
	})
}

// Stats returns a point-in-time server stats snapshot.
func (s *Server) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Connections: len(s.conns)}
}

func (s *Server) acceptLoop(listener net.Listener) {
	for {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		c := conn.New(raw, conn.Config{Limits: s.cfg.Limits}, conn.DispatchFunc(s.dispatch))
		if !s.trackConn(c) {
			continue
		}
		c.Start()
		go s.untrackConnWhenDone(c)
	}
}

func (s *Server) trackConn(c *conn.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		c.Close(core.ErrStopped)
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrackConnWhenDone(c *conn.Conn) {
	<-c.Done()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *Server) dispatch(ctx context.Context, inbound conn.Inbound) {
	switch inbound.Kind {
	case core.FrameKindData, core.FrameKindNotify:
		service := s.service(inbound.ServiceID)
		if service == nil {
			inbound.Payload.Release()
			return
		}
		_ = service.Enqueue(rpc.Request{Payload: inbound.Payload})
	case core.FrameKindRPCRequest:
		s.dispatchRPCRequest(ctx, inbound)
	default:
		inbound.Payload.Release()
	}
}

func (s *Server) dispatchRPCRequest(ctx context.Context, inbound conn.Inbound) {
	service := s.service(inbound.ServiceID)
	if service == nil {
		inbound.Payload.Release()
		return
	}

	reply := make(chan rpc.Response, 1)
	if err := service.Enqueue(rpc.Request{Payload: inbound.Payload, Reply: reply}); err != nil {
		return
	}
	go s.sendRPCResponse(ctx, inbound, reply)
}

func (s *Server) sendRPCResponse(ctx context.Context, inbound conn.Inbound, reply <-chan rpc.Response) {
	select {
	case resp := <-reply:
		status := wire.ResponseOK
		payload := resp.Payload
		if resp.Err != nil {
			status = wire.ResponseErr
			payload = []byte(resp.Err.Error())
		}
		_ = inbound.Conn.Send(ctx, conn.Outbound{
			Kind:      core.FrameKindRPCResponse,
			Priority:  inbound.Priority,
			ServiceID: inbound.ServiceID,
			RequestID: inbound.RequestID,
			Payload:   conn.EncodeRPCResponse(status, payload),
		})
	case <-inbound.Conn.Done():
	case <-s.ctx.Done():
	}
}

func (s *Server) service(serviceID uint16) *rpc.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.services[serviceID]
}
