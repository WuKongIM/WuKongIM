package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrNilRegistry       = errors.New("gateway/core: nil registry")
	ErrNilOptions        = errors.New("gateway/core: nil options")
	ErrDecodeNoProgress  = errors.New("gateway/core: decode returned frames without consuming bytes")
	ErrInvalidDecodeStep = errors.New("gateway/core: decode consumed invalid byte count")
)

const (
	asyncDispatchQueuePerWorker = 1024
	asyncDispatchWorkersPerCPU  = 64
	minAsyncDispatchWorkers     = 64
	maxAsyncDispatchWorkers     = 1024
	// asyncDispatchMinQueuePerWorker keeps enough burst room for one default SEND micro-batch per shard.
	asyncDispatchMinQueuePerWorker = 128
	// asyncDispatchMaxBufferedTasks caps retained SEND queue slots as worker shards scale up.
	asyncDispatchMaxBufferedTasks = maxAsyncDispatchWorkers * asyncDispatchMinQueuePerWorker
)

type Server struct {
	registry   *Registry
	options    gatewaytypes.Options
	dispatcher dispatcher
	sessions   *session.Manager

	nextSessionID atomic.Uint64
	accepting     atomic.Bool

	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	started     bool
	stopped     bool
	listeners   []*listenerRuntime
	states      map[connKey]*sessionState

	// idleTracker schedules read-idle deadlines without scanning all live sessions.
	idleTracker *idleTracker
	// idleMonitorStop stops the shared idle monitor when idle timeouts are enabled.
	idleMonitorStop chan struct{}
	// asyncDispatch bounds SEND frame concurrency off the transport event loop.
	asyncDispatch atomic.Pointer[asyncDispatchQueue]
	workerWG      sync.WaitGroup
}

type listenerRuntime struct {
	options gatewaytypes.ListenerOptions
	factory transport.Factory
	adapter protocol.Adapter
	tracker protocol.ReplyTokenTracker
	// ownsDecodedFrames allows SEND async dispatch to retain decoded payload bytes without copying.
	ownsDecodedFrames bool
	listener          transport.Listener
	eventNetwork      string
	eventProtocol     string
}

type connKey struct {
	listener string
	connID   uint64
}

type sessionState struct {
	server   *Server
	listener *listenerRuntime
	conn     transport.Conn
	session  session.Session
	key      connKey

	inboundMu sync.Mutex
	inbound   []byte

	metaMu           sync.RWMutex
	closeReasonValue gatewaytypes.CloseReason
	authenticated    bool
	authRequired     bool
	openDispatched   bool

	closeOnce sync.Once
	closedCh  chan struct{}

	requestContext       context.Context
	cancelRequestContext context.CancelFunc

	lastReadActivity atomic.Int64
}

// SessionSummary contains aggregate gateway session counts for drain safety.
type SessionSummary struct {
	// GatewaySessions counts all sessions currently tracked by gateway core.
	GatewaySessions int
	// SessionsByListener groups tracked sessions by listener name.
	SessionsByListener map[string]int
	// AcceptingNewSessions reports whether new connection admission is enabled.
	AcceptingNewSessions bool
}

func NewServer(registry *Registry, opts *gatewaytypes.Options) (*Server, error) {
	if registry == nil {
		return nil, ErrNilRegistry
	}
	if opts == nil {
		return nil, ErrNilOptions
	}

	cfg := gatewaytypes.Options{
		Handler:        opts.Handler,
		Authenticator:  opts.Authenticator,
		Observer:       opts.Observer,
		DefaultSession: opts.DefaultSession,
		Transport:      opts.Transport,
		Listeners:      append([]gatewaytypes.ListenerOptions(nil), opts.Listeners...),
		Logger:         opts.Logger,
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	listeners := make([]*listenerRuntime, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		factory, err := registry.Transport(listener.Transport)
		if err != nil {
			return nil, err
		}
		adapter, err := registry.Protocol(listener.Protocol)
		if err != nil {
			return nil, err
		}

		runtime := &listenerRuntime{
			options:       listener,
			factory:       factory,
			adapter:       adapter,
			eventNetwork:  connectionEventNetwork(listener.Network),
			eventProtocol: connectionEventProtocol(listener.Network),
		}
		if tracker, ok := adapter.(protocol.ReplyTokenTracker); ok {
			runtime.tracker = tracker
		}
		if owner, ok := adapter.(protocol.DecodedFrameOwner); ok && owner.OwnsDecodedFrames() {
			runtime.ownsDecodedFrames = true
		}
		listeners = append(listeners, runtime)
	}

	var idleTracker *idleTracker
	if cfg.DefaultSession.IdleTimeout > 0 {
		idleTracker = newIdleTracker(cfg.DefaultSession.IdleTimeout)
	}

	srv := &Server{
		registry:    registry,
		options:     cfg,
		dispatcher:  newDispatcher(cfg.Handler),
		sessions:    session.NewManager(),
		listeners:   listeners,
		states:      make(map[connKey]*sessionState),
		idleTracker: idleTracker,
	}
	srv.accepting.Store(true)
	return srv, nil
}

func (s *Server) Start() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return gatewaytypes.ErrGatewayClosed
	}
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	runtimes := append([]*listenerRuntime(nil), s.listeners...)
	s.mu.Unlock()

	if err := s.buildListeners(runtimes); err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return err
	}

	for _, runtime := range runtimes {
		if err := runtime.listener.Start(); err != nil {
			s.dispatcher.listenerError(runtime.options.Name, err)
			s.rollbackRuntimeListeners(runtimes)
			s.mu.Lock()
			s.started = false
			s.mu.Unlock()
			return err
		}
	}

	s.startIdleMonitor()
	s.startAsyncDispatcher()
	return nil
}

func (s *Server) buildListeners(runtimes []*listenerRuntime) error {
	type listenerGroup struct {
		factory  transport.Factory
		runtimes []*listenerRuntime
	}

	groups := make([]listenerGroup, 0, len(runtimes))
	groupIndex := make(map[string]int, len(runtimes))
	for _, runtime := range runtimes {
		name := runtime.factory.Name()
		idx, ok := groupIndex[name]
		if !ok {
			idx = len(groups)
			groupIndex[name] = idx
			groups = append(groups, listenerGroup{factory: runtime.factory})
		}
		groups[idx].runtimes = append(groups[idx].runtimes, runtime)
	}

	built := make([]transport.Listener, 0, len(runtimes))
	for _, group := range groups {
		specs := make([]transport.ListenerSpec, 0, len(group.runtimes))
		for _, runtime := range group.runtimes {
			runtime := runtime
			specs = append(specs, transport.ListenerSpec{
				Options: transport.ListenerOptions{
					Name:             runtime.options.Name,
					Network:          runtime.options.Network,
					Address:          runtime.options.Address,
					Path:             runtime.options.Path,
					MaxPendingBytes:  s.options.DefaultSession.MaxInboundBytes,
					MaxOutboundBytes: int64(s.options.DefaultSession.MaxOutboundBytes),
					OnError: func(err error) {
						s.dispatcher.listenerError(runtime.options.Name, err)
					},
					Logger: s.options.Logger.Named("transport").Named(runtime.options.Name),
				},
				Handler: &connHandler{server: s, listener: runtime},
			})
		}

		listeners, err := group.factory.Build(specs)
		if err != nil {
			s.rollbackStart(built)
			return err
		}
		if len(listeners) != len(group.runtimes) {
			s.rollbackStart(built)
			return fmt.Errorf("gateway/core: transport %q built %d listeners for %d specs", group.factory.Name(), len(listeners), len(group.runtimes))
		}
		for i, runtime := range group.runtimes {
			runtime.listener = listeners[i]
		}
		built = append(built, listeners...)
	}

	return nil
}

func (s *Server) Stop() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true

	listeners := make([]transport.Listener, 0, len(s.listeners))
	for _, runtime := range s.listeners {
		if runtime.listener != nil {
			listeners = append(listeners, runtime.listener)
		}
	}

	states := make([]*sessionState, 0, len(s.states))
	for _, state := range s.states {
		states = append(states, state)
	}
	idleMonitorStop := s.idleMonitorStop
	s.idleMonitorStop = nil
	asyncDispatch := s.asyncDispatch.Swap(nil)
	s.mu.Unlock()

	if idleMonitorStop != nil {
		close(idleMonitorStop)
	}

	var firstErr error
	for _, listener := range listeners {
		if err := listener.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, state := range states {
		state.close(gatewaytypes.CloseReasonServerStop, nil)
	}
	if asyncDispatch != nil {
		asyncDispatch.close()
	}

	s.workerWG.Wait()
	return firstErr
}

type connHandler struct {
	server   *Server
	listener *listenerRuntime
}

func (h *connHandler) OnOpen(conn transport.Conn) error {
	if h == nil || h.server == nil {
		return nil
	}
	return h.server.onOpen(h.listener, conn)
}

func (h *connHandler) OnData(conn transport.Conn, data []byte) error {
	if h == nil || h.server == nil {
		return nil
	}
	return h.server.onData(h.listener, conn, data)
}

func (h *connHandler) OnClose(conn transport.Conn, err error) {
	if h == nil || h.server == nil {
		return
	}
	h.server.onClose(h.listener, conn, err)
}

func (s *Server) onOpen(listener *listenerRuntime, conn transport.Conn) error {
	if listener == nil || conn == nil {
		return nil
	}
	if !s.AcceptingNewSessions() {
		_ = conn.Close()
		return nil
	}

	state := &sessionState{
		server:   s,
		listener: listener,
		conn:     conn,
		key: connKey{
			listener: listener.options.Name,
			connID:   conn.ID(),
		},
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthRequired(listener.options.Protocol == "wkproto" && s.options.Authenticator != nil)
	if !state.requiresAuth() && listener.options.Protocol != "wsmux" {
		state.setAuthenticated(true)
	}
	state.touchReadActivity()

	sess := session.New(session.Config{
		ID:         s.nextSessionID.Add(1),
		Listener:   listener.options.Name,
		RemoteAddr: conn.RemoteAddr(),
		LocalAddr:  conn.LocalAddr(),
		WriteFrameFn: func(f frame.Frame, meta session.OutboundMeta) error {
			return s.encodeAndWrite(state, f, meta)
		},
	})

	state.session = sess
	if listener.options.Protocol != "wsmux" {
		state.session.SetValue(gatewaytypes.SessionValueProtocolName, listener.options.Protocol)
	}
	s.registerState(state)
	s.observeConnectionOpen(state)

	if err := listener.adapter.OnOpen(sess); err != nil {
		state.close(gatewaytypes.CloseReasonProtocolError, err)
		return nil
	}

	if !state.requiresAuth() && listener.options.Protocol != "wsmux" {
		if err := s.dispatchSessionOpen(state); err != nil {
			s.handleHandlerError(state, err)
		}
		if state.isClosed() {
			return nil
		}
	}

	return nil
}

func (s *Server) onData(listener *listenerRuntime, conn transport.Conn, data []byte) error {
	if listener == nil || conn == nil {
		return nil
	}

	state := s.state(listener.options.Name, conn.ID())
	if state == nil || state.isClosed() {
		return nil
	}

	state.inboundMu.Lock()
	defer state.inboundMu.Unlock()

	if state.isClosed() {
		return nil
	}

	state.touchReadActivity()
	if len(state.inbound) == 0 {
		if limit := s.options.DefaultSession.MaxInboundBytes; limit > 0 && len(data) > limit {
			state.close(gatewaytypes.CloseReasonInboundOverflow, gatewaytypes.ErrInboundOverflow)
			return nil
		}

		frames, consumed, ok := s.decodeInboundFrames(listener, state, data)
		if state.isClosed() {
			return nil
		}
		if !ok {
			state.inbound = append(state.inbound, data...)
			return nil
		}
		if err := s.syncSessionProtocol(state); err != nil {
			s.handleHandlerError(state, err)
			if state.isClosed() {
				return nil
			}
		}
		s.dispatchInboundFrames(listener, state, frames)
		if state.isClosed() || consumed == len(data) {
			return nil
		}
		state.inbound = append(state.inbound, data[consumed:]...)
	} else {
		state.inbound = append(state.inbound, data...)
	}

	if limit := s.options.DefaultSession.MaxInboundBytes; limit > 0 && len(state.inbound) > limit {
		state.close(gatewaytypes.CloseReasonInboundOverflow, gatewaytypes.ErrInboundOverflow)
		return nil
	}

	for !state.isClosed() {
		frames, consumed, ok := s.decodeInboundFrames(listener, state, state.inbound)
		if state.isClosed() || !ok {
			return nil
		}
		if err := s.syncSessionProtocol(state); err != nil {
			s.handleHandlerError(state, err)
			if state.isClosed() {
				return nil
			}
		}

		if consumed == len(state.inbound) {
			state.inbound = nil
		} else {
			state.inbound = state.inbound[consumed:]
		}
		s.dispatchInboundFrames(listener, state, frames)
	}

	return nil
}

func (s *Server) decodeInboundFrames(listener *listenerRuntime, state *sessionState, data []byte) ([]frame.Frame, int, bool) {
	frames, consumed, err := listener.adapter.Decode(state.session, data)
	if err != nil {
		state.close(gatewaytypes.CloseReasonProtocolError, err)
		return nil, 0, false
	}
	if consumed < 0 || consumed > len(data) {
		state.close(gatewaytypes.CloseReasonProtocolError, ErrInvalidDecodeStep)
		return nil, 0, false
	}
	if consumed == 0 && len(frames) == 0 {
		return nil, 0, false
	}
	if consumed == 0 {
		state.close(gatewaytypes.CloseReasonProtocolError, ErrDecodeNoProgress)
		return nil, 0, false
	}
	return frames, consumed, true
}

func (s *Server) dispatchInboundFrames(listener *listenerRuntime, state *sessionState, frames []frame.Frame) {
	tokens := s.replyTokens(listener, state.session, len(frames))
	for i, f := range frames {
		replyToken := ""
		if i < len(tokens) {
			replyToken = tokens[i]
		}
		handled, err := s.handleAuthFrame(state, replyToken, f)
		if err != nil {
			s.handleHandlerError(state, err)
			if state.isClosed() {
				return
			}
			continue
		}
		if handled {
			if state.isClosed() {
				return
			}
			continue
		}
		s.observeFrameIn(state, f)
		if send, ok := isSendPacket(f); ok {
			s.dispatchSendFrameAsync(state, replyToken, send)
			continue
		}
		if err := s.dispatchFrame(state, replyToken, f); err != nil {
			s.handleHandlerError(state, err)
			if state.isClosed() {
				return
			}
		}
	}
}

func isSendPacket(f frame.Frame) (*frame.SendPacket, bool) {
	send, ok := f.(*frame.SendPacket)
	if !ok {
		return nil, false
	}
	return send, true
}

func cloneAsyncSendFrame(send *frame.SendPacket, ownsDecodedFrames bool) frame.Frame {
	if send == nil {
		return nil
	}
	cloned := *send
	if ownsDecodedFrames {
		cloned.Payload = send.Payload[:len(send.Payload):len(send.Payload)]
	} else {
		cloned.Payload = append([]byte(nil), send.Payload...)
	}
	return &cloned
}

func (s *Server) dispatchSendFrameAsync(state *sessionState, replyToken string, send *frame.SendPacket) {
	if s == nil {
		return
	}

	queue := s.asyncDispatcher()
	if queue != nil && queue.submitSend(state, replyToken, send) {
		return
	}
	state.close(gatewaytypes.CloseReasonAsyncDispatchQueueFull, gatewaytypes.ErrAsyncDispatchQueueFull)
}

func (s *Server) dispatchFrame(state *sessionState, replyToken string, f frame.Frame) error {
	start := time.Now()
	err := s.dispatcher.frame(state, replyToken, f)
	s.observeFrameHandled(state, f, time.Since(start), err)
	return err
}

func (s *Server) handleAuthFrame(state *sessionState, replyToken string, f frame.Frame) (bool, error) {
	if state == nil || !state.requiresAuth() || state.isAuthenticated() {
		return false, nil
	}
	start := time.Now()
	status := "fail"
	defer func() {
		s.observeAuth(state, status, time.Since(start))
	}()

	connect, ok := f.(*frame.ConnectPacket)
	if !ok {
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}

	ctx := s.dispatcher.context(state, replyToken, state.closeReason(), nil)
	result, err := s.options.Authenticator.Authenticate(&ctx, connect)
	if err != nil {
		if writeErr := s.writeImmediateFrame(state, &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}); writeErr != nil {
			state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPolicyViolation), writeErr)
			return true, nil
		}
		state.close(gatewaytypes.CloseReasonPolicyViolation, err)
		return true, nil
	}
	if result == nil {
		result = &gatewaytypes.AuthResult{}
	}

	connack := result.Connack
	if connack == nil {
		connack = &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}
	}
	connack.FrameType = frame.CONNACK
	if connack.ReasonCode == 0 {
		connack.ReasonCode = frame.ReasonSuccess
	}

	if connack.ReasonCode != frame.ReasonSuccess {
		if writeErr := s.writeImmediateFrame(state, connack); writeErr != nil {
			state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
			return true, nil
		}
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}

	if result.SessionValues == nil {
		result.SessionValues = make(map[string]any, 1)
	}
	result.SessionValues[gatewaytypes.SessionValueDeviceID] = connect.DeviceID
	for key, value := range result.SessionValues {
		state.session.SetValue(key, value)
	}

	ctx = s.dispatcher.context(state, replyToken, state.closeReason(), nil)
	if activator, ok := s.options.Handler.(gatewaytypes.SessionActivator); ok {
		override, err := activator.OnSessionActivate(&ctx)
		if err != nil {
			if writeErr := s.writeImmediateFrame(state, &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}); writeErr != nil {
				state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPolicyViolation), writeErr)
				return true, nil
			}
			state.close(gatewaytypes.CloseReasonPolicyViolation, err)
			return true, nil
		}
		if override != nil {
			connack = override
		}
	}
	if connack.ReasonCode == 0 {
		connack.ReasonCode = frame.ReasonSuccess
	}

	if writeErr := s.writeImmediateFrame(state, connack); writeErr != nil {
		state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
		return true, nil
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}
	state.setAuthenticated(true)
	status = "ok"
	if !state.openWasDispatched() {
		if err := s.dispatchSessionOpen(state); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (s *Server) onClose(listener *listenerRuntime, conn transport.Conn, err error) {
	if listener == nil || conn == nil {
		return
	}

	state := s.state(listener.options.Name, conn.ID())
	if state == nil {
		return
	}

	if err != nil {
		state.close(gatewaytypes.CloseReasonPeerClosed, err)
		return
	}
	state.close(gatewaytypes.CloseReasonPeerClosed, nil)
}

func (s *Server) encodeAndWrite(state *sessionState, f frame.Frame, meta session.OutboundMeta) error {
	if state == nil || state.listener == nil {
		return session.ErrSessionClosed
	}

	encoded, err := state.listener.adapter.Encode(state.session, f, meta)
	if err != nil {
		return err
	}
	s.observeFrameOut(state, f, len(encoded))
	return s.writePayloadDirect(state, encoded)
}

func (s *Server) writeImmediateFrame(state *sessionState, f frame.Frame) error {
	if state == nil || state.listener == nil {
		return session.ErrSessionClosed
	}

	encoded, err := state.listener.adapter.Encode(state.session, f, session.OutboundMeta{})
	if err != nil {
		return err
	}
	s.observeFrameOut(state, f, len(encoded))
	return s.writePayloadDirect(state, encoded)
}

func (s *Server) dispatchSessionOpen(state *sessionState) error {
	if state == nil {
		return nil
	}

	state.markOpenDispatched()
	return s.dispatcher.sessionOpen(state)
}

// startIdleMonitor starts one shared deadline monitor for all sessions on this server.
func (s *Server) startIdleMonitor() {
	timeout := s.options.DefaultSession.IdleTimeout
	if timeout <= 0 {
		return
	}

	s.mu.Lock()
	if s.stopped || s.idleMonitorStop != nil {
		s.mu.Unlock()
		return
	}
	tracker := s.idleTracker
	if tracker == nil {
		tracker = newIdleTracker(timeout)
		s.idleTracker = tracker
	}
	stopCh := make(chan struct{})
	s.idleMonitorStop = stopCh
	s.workerWG.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.workerWG.Done()

		timer := time.NewTimer(tracker.nextWait(time.Now()))
		defer timer.Stop()

		for {
			select {
			case <-stopCh:
				return
			case now := <-timer.C:
				s.closeIdleSessions(now)
				timer.Reset(tracker.nextWait(time.Now()))
			}
		}
	}()
}

func (s *Server) closeIdleSessions(now time.Time) {
	if s == nil || s.idleTracker == nil {
		return
	}

	for _, state := range s.idleTracker.popExpired(now) {
		if state == nil || state.isClosed() {
			continue
		}
		state.close(gatewaytypes.CloseReasonIdleTimeout, gatewaytypes.ErrIdleTimeout)
	}
}

// startAsyncDispatcher starts a bounded worker pool for SEND dispatch.
func (s *Server) startAsyncDispatcher() {
	if s == nil {
		return
	}

	workers := asyncDispatchWorkerCount(s.options.DefaultSession)
	queue := newAsyncDispatchQueue(workers)

	s.mu.Lock()
	if s.stopped || s.asyncDispatch.Load() != nil {
		s.mu.Unlock()
		return
	}
	s.asyncDispatch.Store(queue)
	s.workerWG.Add(len(queue.shards))
	s.mu.Unlock()

	for i := range queue.shards {
		go s.runAsyncDispatchWorker(queue.shards[i].tasks)
	}
}

func (s *Server) runAsyncDispatchWorker(tasks <-chan asyncDispatchTask) {
	defer s.workerWG.Done()

	collector := newAsyncSendBatchCollector(tasks, asyncSendBatchLimitsFromOptions(s.options.DefaultSession))
	for {
		batch, ok := collector.nextBatch()
		if !ok {
			return
		}
		if s.dispatchSendBatch(batch) {
			continue
		}
		for _, task := range batch {
			recordAsyncDispatchWait(task)
			if err := s.dispatchFrame(task.state, task.replyToken, task.frame); err != nil {
				s.handleHandlerError(task.state, err)
			}
		}
	}
}

func asyncSendBatchLimitsFromOptions(opt gatewaytypes.SessionOptions) asyncSendBatchLimits {
	opt = gatewaytypes.NormalizeSessionOptions(opt)
	return asyncSendBatchLimits{
		maxWait:    opt.AsyncSendBatchMaxWait,
		maxRecords: opt.AsyncSendBatchMaxRecords,
		maxBytes:   opt.AsyncSendBatchMaxBytes,
	}
}

func (s *Server) dispatchSendBatch(batch []asyncDispatchTask) bool {
	if len(batch) == 0 {
		return true
	}
	if !s.dispatcher.canSendBatch() {
		return false
	}
	items := s.sendBatchItems(batch)
	if len(items) != len(batch) {
		return false
	}

	for _, task := range batch {
		recordAsyncDispatchWait(task)
	}
	start := time.Now()
	handled, err := s.dispatcher.sendBatch(items)
	if !handled {
		return false
	}
	elapsed := time.Since(start)
	for _, task := range batch {
		s.observeFrameHandled(task.state, task.frame, elapsed, err)
	}
	if err != nil {
		seen := make(map[*sessionState]struct{}, len(batch))
		for _, task := range batch {
			if task.state == nil {
				continue
			}
			if _, ok := seen[task.state]; ok {
				continue
			}
			seen[task.state] = struct{}{}
			s.handleHandlerError(task.state, err)
		}
	}
	return true
}

func (s *Server) sendBatchItems(batch []asyncDispatchTask) []gatewaytypes.SendBatchItem {
	items := make([]gatewaytypes.SendBatchItem, 0, len(batch))
	for i, task := range batch {
		send, ok := task.frame.(*frame.SendPacket)
		if !ok || send == nil {
			return nil
		}
		var reason gatewaytypes.CloseReason
		requestContext := context.Background()
		if task.state != nil {
			reason = task.state.closeReason()
			requestContext = s.dispatcher.requestContext(task.state)
		}
		items = append(items, gatewaytypes.SendBatchItem{
			Context:    s.dispatcher.context(task.state, task.replyToken, reason, requestContext),
			ReplyToken: task.replyToken,
			Frame:      send,
			Index:      i,
			EnqueuedAt: task.enqueuedAt,
			ByteCount:  asyncDispatchTaskByteCount(task),
		})
	}
	return items
}

func asyncDispatchWorkerCount(opt gatewaytypes.SessionOptions) int {
	if opt.AsyncSendDispatchWorkers > 0 {
		return opt.AsyncSendDispatchWorkers
	}
	return adaptiveAsyncDispatchWorkerCount(runtime.GOMAXPROCS(0))
}

func adaptiveAsyncDispatchWorkerCount(gomaxprocs int) int {
	if gomaxprocs <= 0 {
		return minAsyncDispatchWorkers
	}
	workers := gomaxprocs * asyncDispatchWorkersPerCPU
	if workers < minAsyncDispatchWorkers {
		return minAsyncDispatchWorkers
	}
	if workers > maxAsyncDispatchWorkers {
		return maxAsyncDispatchWorkers
	}
	return workers
}

func (s *Server) asyncDispatcher() *asyncDispatchQueue {
	if s == nil {
		return nil
	}
	return s.asyncDispatch.Load()
}

type asyncDispatchTask struct {
	state      *sessionState
	replyToken string
	frame      frame.Frame
	enqueuedAt time.Time
}

type asyncSendBatchLimits struct {
	maxWait    time.Duration
	maxRecords int
	maxBytes   int
}

type asyncSendBatchCollector struct {
	tasks      <-chan asyncDispatchTask
	limits     asyncSendBatchLimits
	batch      []asyncDispatchTask
	pending    asyncDispatchTask
	hasPending bool
	timer      *time.Timer
}

func newAsyncSendBatchCollector(tasks <-chan asyncDispatchTask, limits asyncSendBatchLimits) *asyncSendBatchCollector {
	if limits.maxRecords <= 0 {
		limits.maxRecords = 1
	}
	return &asyncSendBatchCollector{tasks: tasks, limits: limits}
}

func (c *asyncSendBatchCollector) nextBatch() ([]asyncDispatchTask, bool) {
	if c == nil {
		return nil, false
	}
	first, ok := c.nextTask()
	if !ok {
		return nil, false
	}
	return c.collect(first), true
}

func (c *asyncSendBatchCollector) nextTask() (asyncDispatchTask, bool) {
	if c.hasPending {
		task := c.pending
		c.pending = asyncDispatchTask{}
		c.hasPending = false
		return task, true
	}
	task, ok := <-c.tasks
	return task, ok
}

func (c *asyncSendBatchCollector) collect(first asyncDispatchTask) []asyncDispatchTask {
	c.batch = append(c.batch[:0], first)
	byteCount := asyncDispatchTaskByteCount(first)
	if c.limits.maxRecords <= 1 {
		return c.batch
	}

	var timeout <-chan time.Time
	if c.limits.maxWait > 0 {
		timeout = c.startTimer(c.limits.maxWait)
		defer c.stopTimer()
	}

	for len(c.batch) < c.limits.maxRecords {
		task, ok := c.nextTaskForBatch(timeout)
		if !ok {
			return c.batch
		}
		taskBytes := asyncDispatchTaskByteCount(task)
		if c.limits.maxBytes > 0 && byteCount+taskBytes > c.limits.maxBytes {
			c.pending = task
			c.hasPending = true
			return c.batch
		}
		c.batch = append(c.batch, task)
		byteCount += taskBytes
	}
	return c.batch
}

func (c *asyncSendBatchCollector) nextTaskForBatch(timeout <-chan time.Time) (asyncDispatchTask, bool) {
	if c.hasPending {
		task := c.pending
		c.pending = asyncDispatchTask{}
		c.hasPending = false
		return task, true
	}
	if timeout != nil {
		select {
		case task, ok := <-c.tasks:
			return task, ok
		case <-timeout:
			return asyncDispatchTask{}, false
		}
	}
	select {
	case task, ok := <-c.tasks:
		return task, ok
	default:
		return asyncDispatchTask{}, false
	}
}

func (c *asyncSendBatchCollector) startTimer(d time.Duration) <-chan time.Time {
	if c.timer == nil {
		c.timer = time.NewTimer(d)
		return c.timer.C
	}
	if !c.timer.Stop() {
		select {
		case <-c.timer.C:
		default:
		}
	}
	c.timer.Reset(d)
	return c.timer.C
}

func (c *asyncSendBatchCollector) stopTimer() {
	if c.timer == nil {
		return
	}
	if !c.timer.Stop() {
		select {
		case <-c.timer.C:
		default:
		}
	}
}

func asyncDispatchTaskByteCount(task asyncDispatchTask) int {
	send, ok := task.frame.(*frame.SendPacket)
	if !ok || send == nil {
		return 0
	}
	return len(send.Payload)
}

func recordAsyncDispatchWait(task asyncDispatchTask) {
	if !sendtrace.Enabled() {
		return
	}
	send, ok := task.frame.(*frame.SendPacket)
	if !ok || task.enqueuedAt.IsZero() {
		return
	}
	now := time.Now()
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageGatewayAsyncDispatchWait,
		At:          task.enqueuedAt,
		Duration:    sendtrace.Elapsed(task.enqueuedAt, now),
		ClientMsgNo: send.ClientMsgNo,
	})
}

// asyncDispatchQueue shards SEND work by routing key to avoid a single shared hot queue.
type asyncDispatchQueue struct {
	mu     sync.RWMutex
	shards []asyncDispatchShard
	closed bool
}

type asyncDispatchShard struct {
	tasks chan asyncDispatchTask
}

func newAsyncDispatchQueue(shards int) *asyncDispatchQueue {
	return newAsyncDispatchQueueWithCapacity(shards, asyncDispatchQueueCapacityPerShard(shards))
}

// asyncDispatchQueueCapacityPerShard keeps total buffered SEND slots bounded across many shards.
func asyncDispatchQueueCapacityPerShard(shards int) int {
	if shards <= 0 {
		shards = 1
	}
	capacity := (asyncDispatchMaxBufferedTasks + shards - 1) / shards
	if capacity > asyncDispatchQueuePerWorker {
		return asyncDispatchQueuePerWorker
	}
	if capacity < asyncDispatchMinQueuePerWorker {
		return asyncDispatchMinQueuePerWorker
	}
	return capacity
}

func newAsyncDispatchQueueWithCapacity(shards, capacityPerShard int) *asyncDispatchQueue {
	if shards <= 0 {
		shards = 1
	}
	if capacityPerShard <= 0 {
		capacityPerShard = asyncDispatchQueuePerWorker
	}
	queue := &asyncDispatchQueue{shards: make([]asyncDispatchShard, shards)}
	for i := range queue.shards {
		queue.shards[i].tasks = make(chan asyncDispatchTask, capacityPerShard)
	}
	return queue
}

func (q *asyncDispatchQueue) submitSend(state *sessionState, replyToken string, send *frame.SendPacket) bool {
	if q == nil {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return false
	}
	shard := q.shardForSend(state, send)
	if len(shard.tasks) >= cap(shard.tasks) {
		return false
	}
	task := asyncDispatchTask{
		state:      state,
		replyToken: replyToken,
		frame:      cloneAsyncSendFrame(send, stateOwnsDecodedFrames(state)),
		enqueuedAt: time.Now(),
	}
	select {
	case shard.tasks <- task:
		return true
	default:
		return false
	}
}

func stateOwnsDecodedFrames(state *sessionState) bool {
	return state != nil && state.listener != nil && state.listener.ownsDecodedFrames
}

func (q *asyncDispatchQueue) shardForSend(state *sessionState, send *frame.SendPacket) asyncDispatchShard {
	if q == nil || len(q.shards) == 0 {
		return asyncDispatchShard{}
	}
	return q.shards[asyncDispatchShardIndex(state, send, len(q.shards))]
}

func asyncDispatchShardIndex(state *sessionState, f frame.Frame, shards int) int {
	if shards <= 1 {
		return 0
	}

	hash := uint64(1469598103934665603)
	if send, ok := f.(*frame.SendPacket); ok && send != nil && send.ChannelID != "" {
		hash = asyncDispatchHashString(hash, send.ChannelID)
		hash ^= uint64(send.ChannelType)
		hash *= 1099511628211
		return int(hash % uint64(shards))
	}

	var id uint64
	if state != nil && state.session != nil {
		id = state.session.ID()
	} else if state != nil {
		id = state.key.connID
	}
	if id == 0 {
		return 0
	}
	return int(id % uint64(shards))
}

func asyncDispatchHashString(hash uint64, value string) uint64 {
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	return hash
}

func (q *asyncDispatchQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	for i := range q.shards {
		close(q.shards[i].tasks)
	}
}

func (s *Server) replyTokens(listener *listenerRuntime, sess session.Session, count int) []string {
	if listener == nil || listener.tracker == nil || count <= 0 {
		return nil
	}
	return listener.tracker.TakeReplyTokens(sess, count)
}

func (s *Server) syncSessionProtocol(state *sessionState) error {
	if state == nil || state.openWasDispatched() {
		return nil
	}

	protocol := state.protocolName()
	if protocol == "" || protocol == "wsmux" {
		return nil
	}
	if protocol == "wkproto" && s.options.Authenticator != nil {
		state.setAuthRequired(true)
		return nil
	}

	state.setAuthRequired(false)
	state.setAuthenticated(true)
	return s.dispatchSessionOpen(state)
}

func (s *Server) handleHandlerError(state *sessionState, err error) {
	if state == nil || err == nil {
		return
	}

	reason := closeReasonForError(err, gatewaytypes.CloseReasonHandlerError)
	if s.options.DefaultSession.CloseOnHandlerError == nil || *s.options.DefaultSession.CloseOnHandlerError {
		state.close(reason, err)
		return
	}
	s.dispatcher.sessionError(state, reason, err)
}

func (s *Server) writePayloadDirect(state *sessionState, payload []byte) error {
	if state == nil || state.conn == nil {
		return session.ErrSessionClosed
	}
	if writer, ok := state.conn.(transport.WebSocketMessageWriter); ok {
		if messageType := webSocketMessageTypeForState(state); messageType != transport.WebSocketMessageUnknown {
			return writer.WriteWebSocketMessage(payload, messageType)
		}
	}
	return state.conn.Write(payload)
}

func webSocketMessageTypeForState(state *sessionState) transport.WebSocketMessageType {
	if state == nil || state.listener == nil || state.listener.options.Network != "websocket" {
		return transport.WebSocketMessageUnknown
	}

	protocolName := state.listener.options.Protocol
	if protocolName == "wsmux" && state.session != nil {
		if selected, _ := state.session.Value(gatewaytypes.SessionValueProtocolName).(string); selected != "" {
			protocolName = selected
		}
	}
	switch protocolName {
	case "jsonrpc":
		return transport.WebSocketMessageText
	case "wkproto":
		return transport.WebSocketMessageBinary
	default:
		return transport.WebSocketMessageUnknown
	}
}

func closeReasonForError(err error, fallback gatewaytypes.CloseReason) gatewaytypes.CloseReason {
	switch {
	case errors.Is(err, gatewaytypes.ErrAsyncDispatchQueueFull):
		return gatewaytypes.CloseReasonAsyncDispatchQueueFull
	case errors.Is(err, transport.ErrOutboundBytesExceeded):
		return gatewaytypes.CloseReasonOutboundOverflow
	default:
		return fallback
	}
}

func (s *Server) registerState(state *sessionState) {
	if state == nil || state.session == nil {
		return
	}

	s.mu.Lock()
	s.states[state.key] = state
	s.mu.Unlock()
	s.sessions.Add(state.session)
}

func (s *Server) unregisterState(state *sessionState) {
	if state == nil {
		return
	}
	if s.idleTracker != nil {
		s.idleTracker.remove(state)
	}
	if state.session == nil {
		return
	}

	s.mu.Lock()
	delete(s.states, state.key)
	s.mu.Unlock()
	s.sessions.Remove(state.session.ID())
}

func (s *Server) state(listener string, connID uint64) *sessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[connKey{listener: listener, connID: connID}]
}

// SetAcceptingNewSessions toggles gateway admission for newly opened connections.
func (s *Server) SetAcceptingNewSessions(accepting bool) {
	if s == nil {
		return
	}
	s.accepting.Store(accepting)
}

// AcceptingNewSessions reports whether gateway admission accepts newly opened connections.
func (s *Server) AcceptingNewSessions() bool {
	if s == nil {
		return false
	}
	return s.accepting.Load()
}

// SessionSummary returns aggregate gateway session counts grouped by listener.
func (s *Server) SessionSummary() SessionSummary {
	if s == nil {
		return SessionSummary{SessionsByListener: map[string]int{}}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	summary := SessionSummary{
		SessionsByListener:   make(map[string]int),
		AcceptingNewSessions: s.AcceptingNewSessions(),
	}
	for key := range s.states {
		summary.GatewaySessions++
		summary.SessionsByListener[key.listener]++
	}
	return summary
}

func (s *Server) rollbackStart(listeners []transport.Listener) {
	for i := len(listeners) - 1; i >= 0; i-- {
		_ = listeners[i].Stop()
	}
}

func (s *Server) rollbackRuntimeListeners(runtimes []*listenerRuntime) {
	for i := len(runtimes) - 1; i >= 0; i-- {
		if runtimes[i] != nil && runtimes[i].listener != nil {
			_ = runtimes[i].listener.Stop()
		}
	}
}

func (s *Server) ListenerAddr(name string) string {
	if s == nil || name == "" {
		return ""
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, runtime := range s.listeners {
		if runtime != nil && runtime.options.Name == name && runtime.listener != nil {
			return runtime.listener.Addr()
		}
	}
	return ""
}

func (st *sessionState) close(reason gatewaytypes.CloseReason, err error) {
	if st == nil || st.server == nil {
		return
	}

	st.closeOnce.Do(func() {
		if st.cancelRequestContext != nil {
			st.cancelRequestContext()
		}
		st.setCloseReason(reason)
		st.server.observeConnectionClose(st)
		st.server.unregisterState(st)
		if err != nil && st.openWasDispatched() {
			st.server.dispatcher.sessionError(st, reason, err)
		}
		if st.session != nil {
			_ = st.session.Close()
		}
		if st.listener != nil {
			if closeErr := st.listener.adapter.OnClose(st.session); closeErr != nil {
				st.server.dispatcher.sessionError(st, reason, closeErr)
			}
		}
		if st.conn != nil {
			if closeErr := st.conn.Close(); closeErr != nil {
				st.server.dispatcher.sessionError(st, reason, closeErr)
			}
		}
		if st.openWasDispatched() {
			if closeErr := st.server.dispatcher.sessionClose(st); closeErr != nil {
				st.server.dispatcher.sessionError(st, reason, closeErr)
			}
		}
		close(st.closedCh)
	})
}

func (s *Server) observeConnectionOpen(state *sessionState) {
	if s == nil || s.options.Observer == nil {
		return
	}
	s.options.Observer.OnConnectionOpen(connectionEventForState(state))
}

func (s *Server) observeConnectionClose(state *sessionState) {
	if s == nil || s.options.Observer == nil {
		return
	}
	s.options.Observer.OnConnectionClose(connectionEventForState(state))
}

func (s *Server) observeAuth(state *sessionState, status string, dur time.Duration) {
	if s == nil || s.options.Observer == nil {
		return
	}
	s.options.Observer.OnAuth(gatewaytypes.AuthEvent{
		ConnectionEvent: connectionEventForState(state),
		Status:          status,
		Duration:        dur,
	})
}

func (s *Server) observeFrameIn(state *sessionState, f frame.Frame) {
	if s == nil || s.options.Observer == nil || f == nil {
		return
	}
	s.options.Observer.OnFrameIn(gatewaytypes.FrameEvent{
		ConnectionEvent: connectionEventForState(state),
		FrameType:       f.GetFrameType().String(),
		Bytes:           framePayloadBytes(f),
	})
}

func (s *Server) observeFrameOut(state *sessionState, f frame.Frame, encodedBytes int) {
	if s == nil || s.options.Observer == nil || f == nil {
		return
	}
	bytes := framePayloadBytes(f)
	if bytes == 0 {
		bytes = encodedBytes
	}
	s.options.Observer.OnFrameOut(gatewaytypes.FrameEvent{
		ConnectionEvent: connectionEventForState(state),
		FrameType:       f.GetFrameType().String(),
		Bytes:           bytes,
	})
}

func (s *Server) observeFrameHandled(state *sessionState, f frame.Frame, dur time.Duration, err error) {
	if s == nil || s.options.Observer == nil || f == nil {
		return
	}
	s.options.Observer.OnFrameHandled(gatewaytypes.FrameHandleEvent{
		ConnectionEvent: connectionEventForState(state),
		FrameType:       f.GetFrameType().String(),
		Duration:        dur,
		Err:             err,
	})
}

func connectionEventForState(state *sessionState) gatewaytypes.ConnectionEvent {
	if state == nil || state.listener == nil {
		return gatewaytypes.ConnectionEvent{}
	}
	network := state.listener.eventNetwork
	if network == "" {
		network = connectionEventNetwork(state.listener.options.Network)
	}
	protocol := state.listener.eventProtocol
	if protocol == "" {
		protocol = connectionEventProtocol(state.listener.options.Network)
	}
	return gatewaytypes.ConnectionEvent{
		Listener: state.listener.options.Name,
		Network:  network,
		Protocol: protocol,
	}
}

func connectionEventNetwork(network string) string {
	return strings.ToLower(strings.TrimSpace(network))
}

func connectionEventProtocol(network string) string {
	protocol := connectionEventNetwork(network)
	if protocol == "websocket" {
		return "ws"
	}
	return protocol
}

func framePayloadBytes(f frame.Frame) int {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		return len(pkt.Payload)
	case *frame.RecvPacket:
		return len(pkt.Payload)
	default:
		return 0
	}
}

func (st *sessionState) isClosed() bool {
	if st == nil {
		return true
	}

	select {
	case <-st.closedCh:
		return true
	default:
		return false
	}
}

func (st *sessionState) setCloseReason(reason gatewaytypes.CloseReason) {
	if st == nil {
		return
	}

	st.metaMu.Lock()
	st.closeReasonValue = reason
	st.metaMu.Unlock()
}

func (st *sessionState) closeReason() gatewaytypes.CloseReason {
	if st == nil {
		return ""
	}

	st.metaMu.RLock()
	defer st.metaMu.RUnlock()
	return st.closeReasonValue
}

func (st *sessionState) touchReadActivity() {
	if st == nil {
		return
	}
	now := time.Now()
	st.lastReadActivity.Store(now.UnixNano())
	if st.server != nil && st.server.idleTracker != nil {
		st.server.idleTracker.touch(st, now)
	}
}

func (st *sessionState) setAuthenticated(authenticated bool) {
	if st == nil {
		return
	}

	st.metaMu.Lock()
	st.authenticated = authenticated
	st.metaMu.Unlock()
}

func (st *sessionState) isAuthenticated() bool {
	if st == nil {
		return false
	}

	st.metaMu.RLock()
	defer st.metaMu.RUnlock()
	return st.authenticated
}

func (st *sessionState) setAuthRequired(required bool) {
	if st == nil {
		return
	}

	st.metaMu.Lock()
	st.authRequired = required
	st.metaMu.Unlock()
}

func (st *sessionState) requiresAuth() bool {
	if st == nil {
		return false
	}

	st.metaMu.RLock()
	defer st.metaMu.RUnlock()
	return st.authRequired
}

func (st *sessionState) protocolName() string {
	if st == nil {
		return ""
	}
	if st.session != nil {
		if value, _ := st.session.Value(gatewaytypes.SessionValueProtocolName).(string); value != "" {
			if protocol := strings.TrimSpace(value); protocol != "" {
				return protocol
			}
		}
	}
	if st.listener == nil {
		return ""
	}
	return strings.TrimSpace(st.listener.options.Protocol)
}

func (st *sessionState) markOpenDispatched() {
	if st == nil {
		return
	}

	st.metaMu.Lock()
	st.openDispatched = true
	st.metaMu.Unlock()
}

func (st *sessionState) openWasDispatched() bool {
	if st == nil {
		return false
	}

	st.metaMu.RLock()
	defer st.metaMu.RUnlock()
	return st.openDispatched
}
