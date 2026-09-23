package core

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

// onPacketData retains the transport's bounded buffer but only schedules entry
// work. The independent protocol always requires one completed CONNECT first.
func (s *Server) onPacketData(listener *listenerRuntime, conn transport.Conn, data []byte) error {
	state := s.state(listener.options.Name, conn.ID())
	if state == nil || state.isClosed() {
		return nil
	}
	state.inboundMu.Lock()
	defer state.inboundMu.Unlock()
	if state.isClosed() {
		return nil
	}
	if state.isAuthPending() {
		state.close(gt.CloseReasonPolicyViolation, nil)
		return nil
	}
	limit := s.options.DefaultSession.MaxInboundBytes
	if len(data) > limit-len(state.inbound) {
		state.close(gt.CloseReasonInboundOverflow, gt.ErrInboundOverflow)
		return nil
	}
	input := data
	ownedInput := len(state.inbound) > 0
	if ownedInput {
		state.inbound = append(state.inbound, data...)
		input = state.inbound
	}
	for len(input) > 0 && !state.isClosed() {
		packets, n, err := listener.packetAdapter.DecodePackets(state.session, input)
		if err != nil {
			state.close(gt.CloseReasonProtocolError, err)
			return nil
		}
		if n < 0 || n > len(input) || (n == 0 && len(packets) > 0) {
			state.close(gt.CloseReasonProtocolError, ErrInvalidDecodeStep)
			return nil
		}
		if n == 0 {
			if ownedInput {
				state.inbound = input
			} else {
				// Transport input is borrowed. Once retained, later fragments grow
				// this buffer amortized instead of copying every prefix again.
				state.inbound = append([]byte(nil), input...)
			}
			return nil
		}
		if len(packets) == 0 {
			state.close(gt.CloseReasonProtocolError, ErrDecodeNoProgress)
			return nil
		}
		if !state.isAuthenticated() {
			if len(packets) != 1 || !packets[0].Connect || n != len(input) || !state.beginAuth() {
				state.close(gt.CloseReasonPolicyViolation, nil)
				return nil
			}
			p := packets[0]
			if p.Value == nil || p.Bytes <= 0 || p.Bytes != n {
				state.close(gt.CloseReasonProtocolError, ErrInvalidDecodeStep)
				return nil
			}
			if p.ReadIdleTimeout != nil {
				if *p.ReadIdleTimeout < 0 {
					state.close(gt.CloseReasonProtocolError, ErrInvalidDecodeStep)
					return nil
				}
				state.readIdleOverride.Store(p.ReadIdleTimeout)
			}
			state.touchReadActivity()
			s.observePacketIn(state, p)
			state.inbound = nil
			runtime := s.asyncRuntime()
			var reservation *authPacketReservation
			if s.reservePacketBytes(p.Bytes) {
				reservation = &authPacketReservation{server: s, bytes: p.Bytes}
			}
			accepted := reservation != nil && runtime != nil && runtime.submitAuth(asyncAuthTask{state: state, packet: &p, packetReservation: reservation})
			if !accepted {
				reservation.release()
			}
			var queue asyncAuthStats
			if runtime != nil {
				queue = runtime.auth
			}
			s.observeAsyncAuthAdmission(queue, accepted)
			s.observeAsyncAuthQueue(queue)
			if !accepted {
				state.setAuthPending(false)
				state.close(gt.CloseReasonAsyncAuthQueueFull, gt.ErrAsyncAuthQueueFull)
			}
			return nil
		}
		state.touchReadActivity()
		for _, p := range packets {
			if p.Connect || p.Value == nil || p.Bytes <= 0 || p.Bytes > n {
				state.close(gt.CloseReasonProtocolError, ErrInvalidDecodeStep)
				return nil
			}
			s.observePacketIn(state, p)
			runtime := s.asyncRuntime()
			accepted := runtime != nil && runtime.send != nil && runtime.send.submitPacket(state, p)
			if runtime != nil {
				s.observeAsyncSendAdmission(runtime.send, accepted)
				s.observeAsyncSendQueue(runtime.send)
			}
			if !accepted {
				state.close(gt.CloseReasonAsyncDispatchQueueFull, gt.ErrAsyncDispatchQueueFull)
				return nil
			}
		}
		input = input[n:]
	}
	state.inbound = nil
	return nil
}

// runPacketAuthTask transfers activation cleanup exactly once: to Rollback when
// the handshake aborts, or to the handler's open/close lifecycle after CONNACK.
func (s *Server) runPacketAuthTask(task asyncAuthTask) {
	state := task.state
	if state == nil {
		return
	}
	defer state.setAuthPending(false)
	if state.isClosed() {
		return
	}
	start := time.Now()
	status, failure := authStatusFail, authFailureAuthenticatorError
	defer func() { s.observeAuth(state, status, failure, time.Since(start)) }()
	ctx := s.dispatcher.context(state, "", state.closeReason(), s.dispatcher.requestContext(state))
	result, err := callPacketConnect(s.options.PacketHandler, ctx, task.packet.Value)
	// CONNECT bytes are no longer executor-owned once entry mapping finishes.
	task.packet.Value = nil
	task.packetReservation.release()
	transferred := false
	defer func() {
		if !transferred && result != nil && result.Accepted && result.Rollback != nil {
			if err == nil {
				err = session.ErrSessionClosed
			}
			_ = callPacketCallback(func() error { result.Rollback(err); return nil })
		}
	}()
	if state.isClosed() {
		return
	}
	if err != nil {
		state.close(gt.CloseReasonPolicyViolation, err)
		return
	}
	if result == nil || result.Reply == nil {
		state.close(gt.CloseReasonPolicyViolation, errors.New("gateway: missing packet authentication reply"))
		return
	}
	for key, value := range result.SessionValues {
		state.session.SetValue(key, value)
	}
	if !result.Accepted {
		failure = authFailureConnackAuthFail
		err = ctx.WritePacket(result.Reply)
		state.close(gt.CloseReasonPolicyViolation, err)
		return
	}
	if !state.beginAuthenticatedOpen() {
		return
	}
	if err = ctx.WritePacket(result.Reply); err != nil {
		failure = authFailureConnackWriteError
		state.close(closeReasonForError(err, gt.CloseReasonPeerClosed), err)
		return
	}
	if state.isClosed() {
		return
	}
	status, failure = authStatusOK, authFailureNone
	transferred = true
	if err = s.dispatchSessionOpen(state); err != nil {
		state.close(gt.CloseReasonHandlerError, err)
	}
}

func (s *Server) dispatchPacket(state *sessionState, packet protocol.InboundPacket) error {
	if state == nil || state.isClosed() {
		return nil
	}
	state.waitOpenComplete()
	if state.isClosed() {
		return nil
	}
	ctx := s.dispatcher.context(state, "", state.closeReason(), s.dispatcher.requestContext(state))
	started := time.Now()
	err := callPacketCallback(func() error { return s.options.PacketHandler.OnPacket(ctx, packet.Value) })
	if observer := s.options.Observer; observer != nil {
		observer.OnFrameHandled(gt.FrameHandleEvent{
			ConnectionEvent: connectionEventForState(state),
			FrameType:       state.listener.packetAdapter.PacketName(packet.Value),
			Duration:        time.Since(started), Err: err,
		})
	}
	if errors.Is(err, errPacketCallbackPanic) {
		state.close(gt.CloseReasonHandlerError, err)
	}
	return err
}

func (s *Server) encodeAndWritePacket(state *sessionState, value any, meta session.OutboundMeta) error {
	if state == nil || state.listener == nil || state.listener.packetAdapter == nil {
		return session.ErrPacketWriteUnsupported
	}
	encoded, err := state.listener.packetAdapter.EncodePacket(state.session, value, meta)
	if err != nil {
		return err
	}
	if len(encoded) > s.options.DefaultSession.MaxOutboundBytes {
		return transport.ErrOutboundBytesExceeded
	}
	if err := s.writePayloadDirect(state, encoded); err != nil {
		return err
	}
	if observer := s.options.Observer; observer != nil {
		observer.OnFrameOut(gt.FrameEvent{
			ConnectionEvent: connectionEventForState(state),
			FrameType:       state.listener.packetAdapter.PacketName(value), Bytes: len(encoded),
		})
	}
	return nil
}

func (s *Server) observePacketIn(state *sessionState, packet protocol.InboundPacket) {
	if observer := s.options.Observer; observer != nil {
		observer.OnFrameIn(gt.FrameEvent{
			ConnectionEvent: connectionEventForState(state),
			FrameType:       state.listener.packetAdapter.PacketName(packet.Value), Bytes: packet.Bytes,
		})
	}
}

func (l *listenerRuntime) onOpen(sess session.Session) error {
	if l.packetAdapter != nil {
		return l.packetAdapter.OnOpen(sess)
	}
	return l.adapter.OnOpen(sess)
}

func (s *Server) listenerError(listener *listenerRuntime, err error) {
	if listener == nil || err == nil {
		return
	}
	if listener.packetAdapter != nil {
		_ = callPacketCallback(func() error {
			s.options.PacketHandler.OnListenerError(listener.options.Name, err)
			return nil
		})
		return
	}
	s.dispatcher.listenerError(listener.options.Name, err)
}
func (l *listenerRuntime) onClose(sess session.Session) error {
	if l.packetAdapter != nil {
		return l.packetAdapter.OnClose(sess)
	}
	return l.adapter.OnClose(sess)
}

func (st *sessionState) packetHandler() gt.PacketHandler {
	if st != nil && st.listener != nil && st.listener.packetAdapter != nil && st.server != nil {
		return st.server.options.PacketHandler
	}
	return nil
}

// reservePacketBytes covers both queued and executing independent packet work.
// Auth and dispatch share this budget; a slow callback cannot release capacity
// merely by taking work out of a queue.
func (s *Server) reservePacketBytes(n int) bool {
	if s == nil || n <= 0 {
		return false
	}
	limit := int64(s.options.Runtime.AsyncPacketMaxBytes)
	for {
		used := s.packetBytes.Load()
		if int64(n) > limit-used {
			return false
		}
		if s.packetBytes.CompareAndSwap(used, used+int64(n)) {
			return true
		}
	}
}
func (s *Server) releasePacketBytes(n int) { s.packetBytes.Add(-int64(n)) }

// authPacketReservation can finish before CONNACK opens client admission while
// still ensuring the executor releases it after any earlier panic or close.
type authPacketReservation struct {
	server   *Server
	bytes    int
	released atomic.Bool
}

func (r *authPacketReservation) release() {
	if r != nil && r.released.CompareAndSwap(false, true) {
		r.server.releasePacketBytes(r.bytes)
	}
}

var errPacketCallbackPanic = errors.New("gateway: independent packet callback panicked")

// Entry panics may contain credentials or payloads. The boundary reports only
// a fixed diagnostic and lets normal session cleanup handle the failed owner.
func callPacketCallback(callback func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errPacketCallbackPanic
		}
	}()
	return callback()
}
func callPacketConnect(h gt.PacketHandler, ctx gt.Context, packet any) (result *gt.PacketAuthResult, err error) {
	defer func() {
		if recover() != nil {
			err = errPacketCallbackPanic
		}
	}()
	return h.OnConnect(ctx, packet)
}
