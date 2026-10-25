package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

// Server owns inbound connection and service registry state.
type Server struct {
	// traceRecorder bounds selected inbound request observations independently of service admission.
	traceRecorder *rpctrace.Recorder
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

	// stopOnce starts one physical shutdown, independent of caller budgets.
	stopOnce sync.Once
	// stopDone closes only after connections, requests, workers and observations join.
	stopDone chan struct{}
	// acceptDone joins the listener owner; nil before ListenAndServe.
	acceptDone chan struct{}
	// connWG joins connection cleanup after its read and write loops exit.
	connWG sync.WaitGroup
}

// NewServer validates config and builds a minimal server shell.
func NewServer(cfg ServerConfig) (*Server, error) {
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return nil, err
	}
	var recorder *rpctrace.Recorder
	if normalized.RPCTracing {
		recorder, err = rpctrace.NewRecorder(128)
		if err != nil {
			return nil, err
		}
	}
	observer := core.NewObserverDrain(normalized.Observer, goruntimeregistry.TaskTransportServerObserver)
	if observer != nil {
		normalized.Observer = observer
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:           normalized,
		traceRecorder: recorder,
		observer:      observer,
		ctx:           ctx,
		cancel:        cancel,
		services:      make(map[uint16]*rpc.Service),
		conns:         make(map[*conn.Conn]struct{}),
		stopDone:      make(chan struct{}),
	}, nil
}

// Handle registers handler as the service implementation for serviceID.
func (s *Server) Handle(serviceID uint16, handler Handler, opts ServiceOptions) error {
	if serviceID == wire.CapabilityServiceID || serviceID == wire.TraceCapabilityServiceID {
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
	s.acceptDone = make(chan struct{})
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

// Stop begins shutdown and preserves its bounded compatibility grace. Callers
// that own dependencies must use StopContext and check its result before closing them.
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.StopContext(ctx)
}

// StopContext fences new requests and joins the same shutdown across all calls.
// An expired caller budget returns an incomplete-ownership error; it never
// cancels cleanup or releases dependencies of a handler that is still running.
func (s *Server) StopContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		for _, service := range s.services {
			service.CloseAdmission()
		}
		s.mu.Unlock()
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportServerShutdown, s.shutdown)
	})
	select {
	case <-s.stopDone:
		return nil
	default:
	}
	select {
	case <-s.stopDone:
		return nil
	case <-ctx.Done():
		select {
		case <-s.stopDone:
			return nil
		default:
		}
		return s.incompleteStop(ctx.Err())
	}
}

// shutdown retains every owner until its actual completion, including handlers
// that intentionally ignore request cancellation. No caller can start a second drain.
func (s *Server) shutdown() {
	s.mu.RLock()
	listener, acceptDone, executor := s.listener, s.acceptDone, s.executor
	services := make([]*rpc.Service, 0, len(s.services))
	for _, service := range s.services {
		services = append(services, service)
	}
	conns := make([]*conn.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.RUnlock()
	if listener != nil {
		_ = listener.Close()
	}
	if acceptDone != nil {
		<-acceptDone
	}
	for _, c := range conns {
		c.Close(core.ErrStopped)
	}
	s.connWG.Wait()
	for _, service := range services {
		service.Stop()
	}
	for _, service := range services {
		_ = service.Wait(context.Background())
	}
	if executor != nil {
		_ = executor.Stop()
		_ = executor.Wait(context.Background())
	}
	s.observer.Stop()
	close(s.stopDone)
}

func (s *Server) incompleteStop(cause error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	incomplete := &StopIncompleteError{cause: cause, connections: len(s.conns)}
	for _, service := range s.services {
		requests, retainedBytes := service.Ownership()
		incomplete.requests += requests
		incomplete.retainedBytes += retainedBytes
	}
	return incomplete
}

// Stats returns a point-in-time server stats snapshot.
func (s *Server) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Connections: len(s.conns)}
}

func (s *Server) acceptLoop(listener net.Listener) {
	defer close(s.acceptDone)
	for {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		c := conn.New(raw, conn.Config{
			Limits:         s.cfg.Limits,
			Observer:       s.cfg.Observer,
			NodeID:         s.cfg.NodeID,
			ReceiveTracing: s.cfg.RPCTracing,
		}, conn.DispatchFunc(s.dispatch))
		if !s.trackConn(c) {
			continue
		}
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
	c.Start()
	s.connWG.Add(1)
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrackConnWhenDone(c *conn.Conn) {
	defer s.connWG.Done()
	<-c.Done()
	c.Close(core.ErrStopped)
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
	if inbound.ServiceID == wire.TraceCapabilityServiceID {
		if !s.cfg.RPCTracing {
			inbound.Payload.Release()
			s.sendRPCErrorStatus(ctx, inbound, wire.ResponseServiceNotFound, fmt.Errorf("transport: service %d not found", inbound.ServiceID))
			return
		}
		valid := inbound.Kind == core.FrameKindRPCRequest && inbound.Conn.AcceptRPCTracePayload(inbound.Payload.Bytes())
		if !valid {
			inbound.Payload.Release()
			s.sendRPCError(ctx, inbound, ErrInvalidFrame)
			return
		}
		response := conn.EncodeRPCResponse(wire.ResponseOK, inbound.Payload.Bytes())
		inbound.Payload.Release()
		_ = inbound.Conn.Send(ctx, conn.Outbound{Kind: core.FrameKindRPCResponse, Priority: core.PriorityControl, ServiceID: inbound.ServiceID, RequestID: inbound.RequestID, Payload: response})
		return
	}
	if s.traceRecorder != nil {
		if token, ok := inbound.Conn.TraceIdentity(inbound.RequestID); ok {
			inbound.Trace = s.traceRecorder.Start(ctx, rpctrace.SideServer, inbound.ServiceID)
			if inbound.Trace != nil {
				inbound.Trace.Bind(token, inbound.RequestID)
				if timing := inbound.ReceiveTiming; timing != nil && timing.ServiceID == inbound.ServiceID {
					inbound.Trace.RecordReceivePrefix(timing.FrameReady, timing.ObserverDone)
				}
			}
		}
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
	// Header observation precedes queue ownership so cancellation without handler
	// entry retains its declaration. Missing/dropped traces never classify bytes.
	if inbound.Trace != nil {
		inbound.Trace.RecordOperation(service.TraceOperationFor(inbound.Payload.Bytes()))
	}
	requestCtx = rpctrace.With(requestCtx, inbound.Trace)
	// Encode borrowed handler bytes into an owned wire buffer before the callback returns.
	respond := func(resp rpc.Response) {
		select {
		case <-requestCtx.Done():
			core.FinishRPCTrace(s.cfg.Observer, 0, inbound.Trace, core.RPCTraceResult(requestCtx.Err()))
			return
		case <-inbound.Conn.Done():
			core.FinishRPCTrace(s.cfg.Observer, 0, inbound.Trace, rpctrace.ResultStopped)
			return
		case <-s.ctx.Done():
			core.FinishRPCTrace(s.cfg.Observer, 0, inbound.Trace, rpctrace.ResultStopped)
			return
		default:
		}
		status := wire.ResponseOK
		payload := resp.Payload
		if resp.Err != nil {
			status = responseErrorStatus(resp.Err)
			payload = []byte(resp.Err.Error())
		}
		encode := inbound.Trace.Begin(rpctrace.StageResponseEncode)
		response := conn.EncodeRPCResponse(status, payload)
		encode.End(rpctrace.ResultOK)
		_ = inbound.Conn.Send(ctx, conn.Outbound{
			Kind:      core.FrameKindRPCResponse,
			Priority:  inbound.Priority,
			ServiceID: inbound.ServiceID,
			RequestID: inbound.RequestID,
			Payload:   response, Trace: inbound.Trace, FinishTrace: true, TraceResult: core.RPCTraceResult(resp.Err),
		})
	}

	queue := inbound.Trace.Begin(rpctrace.StageServiceQueue)
	if err := service.Enqueue(rpc.Request{Context: requestCtx, Finish: finish, TraceQueue: queue, Payload: inbound.Payload, RespondBorrowed: respond}); err != nil {
		queue.End(core.RPCTraceResult(err))
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
		core.FinishRPCTrace(s.cfg.Observer, 0, inbound.Trace, rpctrace.ResultStopped)
		return
	case <-s.ctx.Done():
		core.FinishRPCTrace(s.cfg.Observer, 0, inbound.Trace, rpctrace.ResultStopped)
		return
	default:
	}
	encode := inbound.Trace.Begin(rpctrace.StageResponseEncode)
	response := conn.EncodeRPCResponse(status, []byte(err.Error()))
	encode.End(rpctrace.ResultOK)
	_ = inbound.Conn.Send(ctx, conn.Outbound{
		Kind:      core.FrameKindRPCResponse,
		Priority:  inbound.Priority,
		ServiceID: inbound.ServiceID,
		RequestID: inbound.RequestID,
		Payload:   response, Trace: inbound.Trace, FinishTrace: true, TraceResult: core.RPCTraceResult(err),
	})
}

func (s *Server) service(serviceID uint16) *rpc.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stopped {
		return nil
	}
	return s.services[serviceID]
}
