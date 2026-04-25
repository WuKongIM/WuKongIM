package gnet

import (
	"errors"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

type connEventKind uint8

const (
	connEventOpen connEventKind = iota + 1
	connEventData
	connEventClose
)

type connEvent struct {
	kind connEventKind
	data []byte
	err  error
	op   byte
}

type connMode uint8

const (
	connModeTCP connMode = iota + 1
	connModeWSHandshake
	connModeWSFrames
)

type connState struct {
	raw        gnetv2.Conn
	runtime    *listenerRuntime
	transport  *stateConn
	id         uint64
	generation uint64
	localAddr  string
	remoteAddr string

	mu          sync.Mutex
	queue       []connEvent
	wake        chan struct{}
	closing     bool
	notifyClose bool

	mode       connMode
	wsInbound  []byte
	wsFragment []byte
	wsOpcode   byte
	wsWriteOp  byte

	wsCloseSent atomic.Bool
}

func newConnState(id uint64, raw gnetv2.Conn, runtime *listenerRuntime) *connState {
	localAddr := raw.LocalAddr().String()
	mode := connModeTCP
	if runtime != nil {
		if addr := runtime.addr(); addr != "" {
			localAddr = addr
		}
		if runtime.opts.Network == "websocket" {
			mode = connModeWSHandshake
		}
	}

	state := &connState{
		raw:        raw,
		runtime:    runtime,
		id:         id,
		localAddr:  localAddr,
		remoteAddr: raw.RemoteAddr().String(),
		wake:       make(chan struct{}, 1),
		mode:       mode,
	}
	state.transport = &stateConn{state: state}
	return state
}

func (s *connState) start() {
	go s.run()
}

func (s *connState) enqueueOpen() {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.notifyClose = true
	s.queue = append(s.queue, connEvent{kind: connEventOpen})
	s.mu.Unlock()
	s.signal()
}

func (s *connState) enqueueData(data []byte) {
	s.enqueueDataWithOpcode(0, data)
}

func (s *connState) enqueueDataWithOpcode(opcode byte, data []byte) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, connEvent{kind: connEventData, data: data, op: opcode})
	s.mu.Unlock()
	s.signal()
}

func (s *connState) enqueueClose(err error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.queue = append(s.queue, connEvent{kind: connEventClose, err: err})
	s.mu.Unlock()
	s.signal()
}

func (s *connState) fail(err error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.queue = append(s.queue[:0], connEvent{kind: connEventClose, err: err})
	s.mu.Unlock()
	s.signal()
}

func (s *connState) shouldNotifyClose() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifyClose
}

func (s *connState) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *connState) nextEvent() (connEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return connEvent{}, false
	}
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event, true
}

func (s *connState) run() {
	for {
		event, ok := s.nextEvent()
		if !ok {
			<-s.wake
			continue
		}

		switch event.kind {
		case connEventOpen:
			if s.runtime.handler == nil || !s.runtime.shouldDispatch(s) {
				continue
			}
			if err := s.runtime.handler.OnOpen(s.transport); err != nil {
				transport.LogConnectFailure(s.runtime.opts, s.transport.ID(), s.transport.LocalAddr(), s.transport.RemoteAddr(), err)
				s.fail(err)
				_ = s.raw.Close()
				continue
			}
			transport.LogConnectSuccess(s.runtime.opts, s.transport)
		case connEventData:
			if s.runtime.handler == nil || !s.runtime.shouldDispatch(s) {
				continue
			}
			if event.op == wsOpcodeText || event.op == wsOpcodeBinary {
				s.wsWriteOp = event.op
			}
			if err := s.runtime.handler.OnData(s.transport, event.data); err != nil {
				s.fail(err)
				_ = s.raw.Close()
			}
		case connEventClose:
			if s.runtime.handler != nil && s.shouldNotifyClose() {
				s.runtime.handler.OnClose(s.transport, event.err)
			}
			return
		}
	}
}

func (s *connState) currentMode() connMode {
	return s.mode
}

func (s *connState) appendWSInbound(data []byte) {
	s.wsInbound = append(s.wsInbound, data...)
}

func (s *connState) consumeWSHandshake() (*wsHandshakeResult, *wsHandshakeFailure, bool) {
	result, failure, complete := parseWSHandshake(s.wsInbound, s.runtime.opts.Path)
	if !complete {
		return nil, nil, false
	}
	if result != nil {
		s.wsInbound = append(s.wsInbound[:0], s.wsInbound[result.consumed:]...)
		s.mode = connModeWSFrames
	} else {
		s.wsInbound = s.wsInbound[:0]
	}
	return result, failure, true
}

type wsTrafficResult struct {
	payload    []byte
	opcode     byte
	write      []byte
	closeWrite []byte
	closeNow   bool
	closeErr   error
}

func (s *connState) nextWSResult() (wsTrafficResult, bool) {
	for {
		frame, consumed, err := decodeWSFrame(s.wsInbound)
		if errors.Is(err, errWSNeedMoreData) {
			return wsTrafficResult{}, false
		}
		if err != nil {
			return wsTrafficResult{
				closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
				closeNow:   true,
				closeErr:   err,
			}, true
		}

		s.wsInbound = s.wsInbound[consumed:]
		if !frame.masked {
			err := newWSProtocolError(wsCloseProtocolError, "client websocket frames must be masked")
			return wsTrafficResult{
				closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
				closeNow:   true,
				closeErr:   err,
			}, true
		}

		switch frame.opcode {
		case wsOpcodeContinuation:
			if s.wsOpcode == 0 {
				err := newWSProtocolError(wsCloseProtocolError, "unexpected websocket continuation frame")
				return wsTrafficResult{
					closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
					closeNow:   true,
					closeErr:   err,
				}, true
			}
			s.wsFragment = append(s.wsFragment, frame.payload...)
			if !frame.final {
				continue
			}
			payload := append([]byte(nil), s.wsFragment...)
			opcode := s.wsOpcode
			s.wsFragment = s.wsFragment[:0]
			s.wsOpcode = 0
			if opcode == wsOpcodeText && !utf8.Valid(payload) {
				err := newWSProtocolError(wsCloseInvalidData, "invalid utf-8 websocket text payload")
				return wsTrafficResult{
					closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
					closeNow:   true,
					closeErr:   err,
				}, true
			}
			return wsTrafficResult{payload: payload, opcode: opcode}, true
		case wsOpcodeText, wsOpcodeBinary:
			if s.wsOpcode != 0 {
				err := newWSProtocolError(wsCloseProtocolError, "websocket message started before fragmented message completed")
				return wsTrafficResult{
					closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
					closeNow:   true,
					closeErr:   err,
				}, true
			}
			if !frame.final {
				s.wsOpcode = frame.opcode
				s.wsFragment = append(s.wsFragment[:0], frame.payload...)
				continue
			}
			if frame.opcode == wsOpcodeText && !utf8.Valid(frame.payload) {
				err := newWSProtocolError(wsCloseInvalidData, "invalid utf-8 websocket text payload")
				return wsTrafficResult{
					closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
					closeNow:   true,
					closeErr:   err,
				}, true
			}
			return wsTrafficResult{payload: append([]byte(nil), frame.payload...), opcode: frame.opcode}, true
		case wsOpcodePing:
			pong, err := encodeWSFrame(wsFrame{
				final:   true,
				opcode:  wsOpcodePong,
				payload: append([]byte(nil), frame.payload...),
			})
			if err != nil {
				return wsTrafficResult{closeNow: true, closeErr: err}, true
			}
			return wsTrafficResult{write: pong}, true
		case wsOpcodePong:
			continue
		case wsOpcodeClose:
			if err := validWSClosePayload(frame.payload); err != nil {
				return wsTrafficResult{
					closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
					closeNow:   true,
					closeErr:   err,
				}, true
			}

			var closeWrite []byte
			if s.wsCloseSent.CompareAndSwap(false, true) {
				payload := append([]byte(nil), frame.payload...)
				if len(payload) == 0 {
					payload = []byte{byte(wsCloseNormalClosure >> 8), byte(wsCloseNormalClosure & 0xff)}
				}
				closeWrite, _ = encodeWSFrame(wsFrame{
					final:   true,
					opcode:  wsOpcodeClose,
					payload: payload,
				})
			}
			return wsTrafficResult{closeWrite: closeWrite, closeNow: true}, true
		default:
			err := newWSProtocolError(wsCloseProtocolError, "unsupported websocket opcode")
			return wsTrafficResult{
				closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
				closeNow:   true,
				closeErr:   err,
			}, true
		}
	}
}

type stateConn struct {
	state *connState
}

func (c *stateConn) ID() uint64 {
	return c.state.id
}

func (c *stateConn) Write(data []byte) error {
	payload := append([]byte(nil), data...)
	if c.state.runtime != nil && c.state.runtime.opts.Network == "websocket" {
		opcode := c.state.wsWriteOp
		if opcode != wsOpcodeText && opcode != wsOpcodeBinary {
			opcode = byte(wsOpcodeBinary)
			if utf8.Valid(payload) {
				opcode = wsOpcodeText
			}
		}
		framed, err := encodeWSFrame(wsFrame{
			final:   true,
			opcode:  opcode,
			payload: payload,
		})
		if err != nil {
			return err
		}
		payload = framed
	}
	return c.state.raw.AsyncWrite(payload, nil)
}

func (c *stateConn) Close() error {
	if c.state.runtime != nil && c.state.runtime.opts.Network == "websocket" && c.state.wsCloseSent.CompareAndSwap(false, true) {
		frame := buildWSCloseFrame(wsCloseNormalClosure, "")
		if len(frame) > 0 {
			if err := c.state.raw.AsyncWrite(frame, func(conn gnetv2.Conn, err error) error {
				return conn.Close()
			}); err == nil {
				return nil
			}
		}
	}
	return c.state.raw.Close()
}

func (c *stateConn) LocalAddr() string {
	return c.state.localAddr
}

func (c *stateConn) RemoteAddr() string {
	return c.state.remoteAddr
}

var _ transport.Conn = (*stateConn)(nil)
