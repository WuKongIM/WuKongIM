package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

// Server owns inbound connection and service registry state.
type Server struct {
	// cfg stores normalized inbound transport settings.
	cfg ServerConfig
	// observer drains transport events away from service and connection hot paths.
	observer *core.ObserverDrain

	// ctx is canceled when Stop begins server shutdown.
	ctx context.Context
	// cancel stops server-owned dispatch response goroutines.
	cancel context.CancelFunc

	// mu protects listener, services, executor, conns, and stopped.
	mu sync.RWMutex
	// listener accepts inbound transport connections after ListenAndServe.
	listener net.Listener
	// services maps service ids to registered RPC services.
	services map[uint16]*rpc.Service
	// executor runs registered service handlers on a shared bounded ants pool.
	executor *rpc.Executor
	// serviceConcurrency is the total registered service handler concurrency.
	serviceConcurrency int
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
	observer := core.NewObserverDrain(normalized.Observer, goruntimeregistry.TaskTransportServerObserver)
	if observer != nil {
		normalized.Observer = observer
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:      normalized,
		observer: observer,
		ctx:      ctx,
		cancel:   cancel,
		services: make(map[uint16]*rpc.Service),
		conns:    make(map[*conn.Conn]struct{}),
	}, nil
}

// Handle registers handler as the service implementation for serviceID.
func (s *Server) Handle(serviceID uint16, handler Handler, opts ServiceOptions) error {
	if serviceID == wire.CapabilityServiceID {
		return fmt.Errorf("%w: reserved transport capability service", ErrInvalidConfig)
	}
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
	executor, err := s.serviceExecutorLocked(opts.Concurrency)
	if err != nil {
		return err
	}
	s.services[serviceID] = rpc.NewServiceWithExecutor(serviceID, handler, opts, s.cfg.Observer, executor)
	return nil
}

func (s *Server) serviceExecutorLocked(concurrency int) (*rpc.Executor, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	if s.executor == nil {
		executor, err := rpc.NewExecutor(concurrency, s.cfg.Observer)
		if err != nil {
			return nil, err
		}
		s.executor = executor
		s.serviceConcurrency = concurrency
		return executor, nil
	}
	s.serviceConcurrency += concurrency
	s.executor.Tune(s.serviceConcurrency)
	return s.executor, nil
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

	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportAccept, func() { s.acceptLoop(listener) })
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
		executor := s.executor
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
		if executor != nil {
			_ = executor.Stop()
		}
		s.observer.Stop()
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
		c := conn.New(raw, conn.Config{
			Limits:   s.cfg.Limits,
			Observer: s.cfg.Observer,
			NodeID:   s.cfg.NodeID,
		}, conn.DispatchFunc(s.dispatch))
		if !s.trackConn(c) {
			continue
		}
		c.Start()
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportConnectionCleanup, func() { s.untrackConnWhenDone(c) })
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
	case core.FrameKindRPCCancel:
		inbound.Payload.Release()
		inbound.Conn.CancelInbound(inbound.RequestID)
	case core.FrameKindRPCRequest, core.FrameKindRPCBudgetRequest:
		s.dispatchRPCRequest(ctx, inbound)
	default:
		inbound.Payload.Release()
	}
}

func (s *Server) dispatchRPCRequest(ctx context.Context, inbound conn.Inbound) {
	if inbound.ServiceID == wire.CapabilityServiceID {
		valid := inbound.Kind == core.FrameKindRPCRequest && string(inbound.Payload.Bytes()) == wire.CapabilityRequestBudgets
		inbound.Payload.Release()
		if !valid {
			s.sendRPCError(ctx, inbound, ErrInvalidFrame)
			return
		}
		_ = inbound.Conn.Send(ctx, conn.Outbound{Kind: core.FrameKindRPCResponse, Priority: core.PriorityControl, ServiceID: inbound.ServiceID, RequestID: inbound.RequestID, Payload: conn.EncodeRPCResponse(wire.ResponseOK, []byte(wire.CapabilityRequestBudgets))})
		return
	}
	service := s.service(inbound.ServiceID)
	if service == nil {
		inbound.Payload.Release()
		s.sendRPCErrorStatus(
			ctx,
			inbound,
			wire.ResponseServiceNotFound,
			fmt.Errorf("transport: service %d not found", inbound.ServiceID),
		)
		return
	}

	requestCtx, finish, err := inbound.Conn.TrackInbound(inbound.RequestID, time.Duration(inbound.BudgetMillis)*time.Millisecond)
	if err != nil {
		inbound.Payload.Release()
		s.sendRPCError(ctx, inbound, err)
		return
	}
	// Encode borrowed handler bytes into an owned wire buffer before the callback returns.
	respond := func(resp rpc.Response) {
		select {
		case <-requestCtx.Done():
			return
		case <-inbound.Conn.Done():
			return
		case <-s.ctx.Done():
			return
		default:
		}
		status := wire.ResponseOK
		payload := resp.Payload
		if resp.Err != nil {
			status = responseErrorStatus(resp.Err)
			payload = []byte(resp.Err.Error())
		}
		_ = inbound.Conn.Send(ctx, conn.Outbound{
			Kind:      core.FrameKindRPCResponse,
			Priority:  inbound.Priority,
			ServiceID: inbound.ServiceID,
			RequestID: inbound.RequestID,
			Payload:   conn.EncodeRPCResponse(status, payload),
		})
	}

	if err := service.Enqueue(rpc.Request{Context: requestCtx, Finish: finish, Payload: inbound.Payload, RespondBorrowed: respond}); err != nil {
		finish()
		s.sendRPCError(ctx, inbound, err)
		return
	}
}

func (s *Server) sendRPCError(ctx context.Context, inbound conn.Inbound, err error) {
	s.sendRPCErrorStatus(ctx, inbound, responseErrorStatus(err), err)
}

// responseErrorStatus preserves known transport failures; arbitrary handler
// messages remain generic even when their text resembles a timeout or overload.
func responseErrorStatus(err error) uint8 {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return wire.ResponseTimeout
	case errors.Is(err, ErrCanceled), errors.Is(err, context.Canceled):
		return wire.ResponseCanceled
	case errors.Is(err, ErrBusy), errors.Is(err, ErrQueueFull):
		return wire.ResponseBusy
	case errors.Is(err, ErrStopped):
		return wire.ResponseStopped
	default:
		return wire.ResponseErr
	}
}

func (s *Server) sendRPCErrorStatus(ctx context.Context, inbound conn.Inbound, status uint8, err error) {
	select {
	case <-inbound.Conn.Done():
		return
	case <-s.ctx.Done():
		return
	default:
	}
	_ = inbound.Conn.Send(ctx, conn.Outbound{
		Kind:      core.FrameKindRPCResponse,
		Priority:  inbound.Priority,
		ServiceID: inbound.ServiceID,
		RequestID: inbound.RequestID,
		Payload:   conn.EncodeRPCResponse(status, []byte(err.Error())),
	})
}

func (s *Server) service(serviceID uint16) *rpc.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.services[serviceID]
}
