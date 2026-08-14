package gnet

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
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

// ErrPendingBytesExceeded indicates that transport-owned inbound buffering exceeded its configured limit.
var ErrPendingBytesExceeded = errors.New("gateway/transport/gnet: pending inbound bytes limit exceeded")

var errOutboundSubmissionClosed = errors.New("gateway/transport/gnet: outbound submission is closed")

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

	mu               sync.Mutex
	queue            []connEvent
	pendingBytes     int
	inboundRevision  uint64
	maxPendingBytes  int
	maxOutboundBytes int64
	owner            *actorShard // owner serializes handler callbacks for this connection.
	scheduled        atomic.Bool
	closing          bool
	notifyClose      bool
	pressureMu       sync.Mutex
	pressureClosed   bool

	mode       connMode
	wsInbound  []byte
	wsFragment []byte
	wsOpcode   byte

	wsWriteOp   atomic.Uint32
	wsCloseSent atomic.Bool

	// outboundSubmitMu preserves raw submission order and fences later writes
	// after an ambiguous asynchronous trigger error.
	outboundSubmitMu       sync.Mutex
	outboundSubmitErr      error
	outboundMu             sync.Mutex
	outboundPendingBytes   int64
	outboundBufferedBytes  int64
	outboundRevision       uint64
	outboundWriteFrameFree []*wsWritevFrame
	outboundWrites         []outboundWrite
}

// outboundWrite owns one raw gnet submission until that submission's callback.
// frame is non-nil only for vector WebSocket writes whose header slice must not
// be reset or reused while gnet can still reference it.
type outboundWrite struct {
	size  int
	frame *wsWritevFrame
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
		mode:       mode,
	}
	if runtime != nil {
		state.maxPendingBytes = runtime.opts.MaxPendingBytes
		state.maxOutboundBytes = runtime.opts.MaxOutboundBytes
	}
	state.transport = &stateConn{state: state}
	return state
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

func (s *connState) enqueueData(data []byte) bool {
	return s.enqueueDataWithOpcode(0, data)
}

func (s *connState) enqueueDataWithOpcode(opcode byte, data []byte) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	if s.maxPendingBytes > 0 && s.pendingBytes+len(data) > s.maxPendingBytes {
		depth := len(s.queue)
		bytes := int64(s.pendingBytes + len(data))
		bytesCapacity := int64(s.maxPendingBytes)
		s.mu.Unlock()
		s.observeTransport("inbound_pending", "inbound", depth, 0, bytes, bytesCapacity, "too_large")
		return false
	}
	s.pendingBytes += len(data)
	s.queue = append(s.queue, connEvent{kind: connEventData, data: data, op: opcode})
	s.inboundRevision++
	depth := len(s.queue)
	bytes := int64(s.pendingBytes)
	bytesCapacity := int64(s.maxPendingBytes)
	snapshot := transportPressureSnapshot{
		name:          "inbound_pending",
		queue:         "inbound",
		depth:         depth,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		result:        "ok",
		revision:      s.inboundRevision,
	}
	s.mu.Unlock()
	s.signal()
	s.observeTransportSnapshot(snapshot)
	return true
}

// enqueueCopiedData copies from gnet's transient read buffer only after pending-byte admission succeeds.
func (s *connState) enqueueCopiedData(data []byte) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	if s.maxPendingBytes > 0 && s.pendingBytes+len(data) > s.maxPendingBytes {
		depth := len(s.queue)
		bytes := int64(s.pendingBytes + len(data))
		bytesCapacity := int64(s.maxPendingBytes)
		s.mu.Unlock()
		s.observeTransport("inbound_pending", "inbound", depth, 0, bytes, bytesCapacity, "too_large")
		return false
	}
	payload := append([]byte(nil), data...)
	s.pendingBytes += len(payload)
	s.queue = append(s.queue, connEvent{kind: connEventData, data: payload})
	s.inboundRevision++
	depth := len(s.queue)
	bytes := int64(s.pendingBytes)
	bytesCapacity := int64(s.maxPendingBytes)
	snapshot := transportPressureSnapshot{
		name:          "inbound_pending",
		queue:         "inbound",
		depth:         depth,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		result:        "ok",
		revision:      s.inboundRevision,
	}
	s.mu.Unlock()
	s.signal()
	s.observeTransportSnapshot(snapshot)
	return true
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
	s.pendingBytes = 0
	for i := range s.queue {
		s.queue[i] = connEvent{}
	}
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
	if s == nil || s.owner == nil {
		return
	}
	s.owner.schedule(s)
}

func (s *connState) nextEvent() (connEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return connEvent{}, false
	}
	event := s.queue[0]
	s.queue[0] = connEvent{}
	s.queue = s.queue[1:]
	return event, true
}

func (s *connState) releaseEvent(event connEvent) {
	if len(event.data) == 0 {
		return
	}
	s.mu.Lock()
	s.pendingBytes -= len(event.data)
	if s.pendingBytes < 0 {
		s.pendingBytes = 0
	}
	s.inboundRevision++
	depth := len(s.queue)
	bytes := int64(s.pendingBytes)
	bytesCapacity := int64(s.maxPendingBytes)
	snapshot := transportPressureSnapshot{
		name:          "inbound_pending",
		queue:         "inbound",
		depth:         depth,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		revision:      s.inboundRevision,
	}
	s.mu.Unlock()
	if s.maxPendingBytes > 0 {
		s.observeTransportSnapshot(snapshot)
	}
}

func (s *connState) processReady() {
	for {
		event, ok := s.nextEvent()
		if !ok {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.scheduled.Store(false)
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		if done := s.handleEvent(event); done {
			s.scheduled.Store(false)
			return
		}
	}
}

func (s *connState) handleEvent(event connEvent) bool {
	switch event.kind {
	case connEventOpen:
		if s.runtime.handler == nil || !s.runtime.shouldDispatch(s) {
			return false
		}
		if err := s.runtime.handler.OnOpen(s.transport); err != nil {
			transport.LogConnectFailure(s.runtime.opts, s.transport.ID(), s.transport.LocalAddr(), s.transport.RemoteAddr(), err)
			s.fail(err)
			_ = s.raw.Close()
			return false
		}
		transport.LogConnectSuccess(s.runtime.opts, s.transport)
	case connEventData:
		if s.runtime.handler == nil || !s.runtime.shouldDispatch(s) {
			s.releaseEvent(event)
			return false
		}
		if event.op == wsOpcodeText || event.op == wsOpcodeBinary {
			s.wsWriteOp.Store(uint32(event.op))
		}
		if err := s.runtime.handler.OnData(s.transport, event.data); err != nil {
			s.fail(err)
			_ = s.raw.Close()
		}
		s.releaseEvent(event)
	case connEventClose:
		if s.runtime.handler != nil && s.shouldNotifyClose() {
			s.runtime.handler.OnClose(s.transport, event.err)
		}
		s.abandonOutboundWrites()
		s.closeTransportPressure()
		return true
	}
	return false
}

func (s *connState) currentMode() connMode {
	return s.mode
}

func (s *connState) appendWSInbound(data []byte) bool {
	if s.maxPendingBytes > 0 && len(s.wsInbound)+len(data) > s.maxPendingBytes+wsMaxFrameHeaderBytes {
		return false
	}
	s.wsInbound = append(s.wsInbound, data...)
	return true
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
		frame, consumed, err := decodeWSFrameWithLimit(s.wsInbound, s.maxPendingBytes)
		if errors.Is(err, errWSNeedMoreData) {
			return wsTrafficResult{}, false
		}
		if errors.Is(err, ErrPendingBytesExceeded) {
			return wsTrafficResult{closeNow: true, closeErr: err}, true
		}
		if err != nil {
			return wsTrafficResult{
				closeWrite: buildWSCloseFrame(wsCloseCodeForErr(err), err.Error()),
				closeNow:   true,
				closeErr:   err,
			}, true
		}

		if consumed == len(s.wsInbound) {
			s.wsInbound = s.wsInbound[:0]
		} else {
			s.wsInbound = s.wsInbound[consumed:]
		}
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
			if s.maxPendingBytes > 0 && len(s.wsFragment)+len(frame.payload) > s.maxPendingBytes {
				return wsTrafficResult{closeNow: true, closeErr: ErrPendingBytesExceeded}, true
			}
			s.wsFragment = append(s.wsFragment, frame.payload...)
			if !frame.final {
				continue
			}
			payload := s.wsFragment
			opcode := s.wsOpcode
			s.wsFragment = nil
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
			return wsTrafficResult{payload: frame.payload, opcode: frame.opcode}, true
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

// wsWritevPayloadThreshold keeps compact small-message writes while avoiding large websocket payload copies.
const wsWritevPayloadThreshold = 1024

func (c *stateConn) ID() uint64 {
	return c.state.id
}

func (c *stateConn) Write(data []byte) error {
	if c.state.runtime != nil && c.state.runtime.opts.Network == "websocket" {
		return c.writeWebSocket(data, transport.WebSocketMessageUnknown)
	}

	if c.state.maxOutboundBytes <= 0 || len(data) == 0 {
		return c.state.raw.AsyncWrite(data, nil)
	}
	return c.asyncWriteWithOutboundLimit(len(data), func(callback gnetv2.AsyncCallback) error {
		return c.state.raw.AsyncWrite(data, callback)
	})
}

func (c *stateConn) WriteWebSocketMessage(data []byte, messageType transport.WebSocketMessageType) error {
	if c.state.runtime == nil || c.state.runtime.opts.Network != "websocket" {
		return c.Write(data)
	}
	if len(data) < wsWritevPayloadThreshold {
		return c.writeWebSocketCompact(data, messageType)
	}
	return c.writeWebSocketVector(data, messageType)
}

func (c *stateConn) writeWebSocket(data []byte, messageType transport.WebSocketMessageType) error {
	if len(data) < wsWritevPayloadThreshold {
		return c.writeWebSocketCompact(data, messageType)
	}
	return c.writeWebSocketVector(data, messageType)
}

func (c *stateConn) writeWebSocketCompact(data []byte, messageType transport.WebSocketMessageType) error {
	frame := wsFrame{
		final:   true,
		opcode:  c.webSocketWriteOpcode(messageType),
		payload: data,
	}
	framed, err := encodeWSFrame(frame)
	if err != nil {
		return err
	}
	if c.state.maxOutboundBytes <= 0 || len(framed) == 0 {
		return c.state.raw.AsyncWrite(framed, nil)
	}
	return c.asyncWriteWithOutboundLimit(len(framed), func(callback gnetv2.AsyncCallback) error {
		return c.state.raw.AsyncWrite(framed, callback)
	})
}

func (c *stateConn) writeWebSocketVector(data []byte, messageType transport.WebSocketMessageType) error {
	frame := wsFrame{
		final:   true,
		opcode:  c.webSocketWriteOpcode(messageType),
		payload: data,
	}
	framed := c.state.acquireOutboundWriteFrame()
	err := buildWSWritevFrame(frame, framed)
	if err != nil {
		c.state.releaseOutboundWriteFrame(framed)
		return err
	}
	c.state.outboundSubmitMu.Lock()
	defer c.state.outboundSubmitMu.Unlock()
	if c.state.outboundSubmitErr != nil {
		c.state.releaseOutboundWriteFrame(framed)
		return c.state.outboundSubmitErr
	}
	size := len(framed.bufs[0]) + len(framed.bufs[1])
	snapshot, admitted := c.state.beginOutboundWrite(size, framed)
	if !admitted {
		c.state.releaseOutboundWriteFrame(framed)
		return transport.ErrOutboundBytesExceeded
	}
	err = c.state.raw.AsyncWritev(framed.bufs[:], releaseOutboundWriteCallback)
	c.state.observeTransportSnapshot(snapshot)
	if err != nil {
		// gnet may have queued the callback-owned frame before its wake syscall
		// reported an error. Keep ownership with that callback (or connection
		// close) and reject later submissions so FIFO reservations cannot skew.
		c.state.outboundSubmitErr = err
	}
	return err
}

func (c *stateConn) webSocketWriteOpcode(messageType transport.WebSocketMessageType) byte {
	switch messageType {
	case transport.WebSocketMessageText:
		return wsOpcodeText
	case transport.WebSocketMessageBinary:
		return wsOpcodeBinary
	default:
	}
	if opcode := byte(c.state.wsWriteOp.Load()); opcode == wsOpcodeText || opcode == wsOpcodeBinary {
		return opcode
	}
	return wsOpcodeBinary
}

func (c *stateConn) asyncWriteWithOutboundLimit(size int, write func(gnetv2.AsyncCallback) error) error {
	c.state.outboundSubmitMu.Lock()
	defer c.state.outboundSubmitMu.Unlock()
	if c.state.outboundSubmitErr != nil {
		return c.state.outboundSubmitErr
	}
	if c.state.maxOutboundBytes <= 0 || size <= 0 {
		return write(nil)
	}
	snapshot, admitted := c.state.beginOutboundWrite(size, nil)
	if !admitted {
		return transport.ErrOutboundBytesExceeded
	}
	err := write(releaseOutboundWriteCallback)
	c.state.observeTransportSnapshot(snapshot)
	if err != nil {
		// A non-nil gnet Trigger result is ambiguous: the callback task may
		// already be queued. Preserve its FIFO reservation until callback/close.
		c.state.outboundSubmitErr = err
	}
	return err
}

func (s *connState) beginOutboundWrite(size int, frame *wsWritevFrame) (transportPressureSnapshot, bool) {
	s.outboundMu.Lock()
	if s.maxOutboundBytes > 0 && size > 0 && s.outboundPendingBytes+s.outboundBufferedBytes+int64(size) > s.maxOutboundBytes {
		depth := len(s.outboundWrites)
		bytes := s.outboundPendingBytes + s.outboundBufferedBytes + int64(size)
		bytesCapacity := s.maxOutboundBytes
		s.outboundMu.Unlock()
		s.observeTransport("outbound_pending", "outbound", depth, 0, bytes, bytesCapacity, "full")
		return transportPressureSnapshot{}, false
	}
	reservedSize := 0
	if s.maxOutboundBytes > 0 && size > 0 {
		reservedSize = size
		s.outboundPendingBytes += int64(size)
	}
	s.outboundWrites = append(s.outboundWrites, outboundWrite{size: reservedSize, frame: frame})
	if s.maxOutboundBytes <= 0 {
		s.outboundMu.Unlock()
		return transportPressureSnapshot{}, true
	}
	s.outboundRevision++
	revision := s.outboundRevision
	depth := len(s.outboundWrites)
	bytes := s.outboundPendingBytes + s.outboundBufferedBytes
	bytesCapacity := s.maxOutboundBytes
	s.outboundMu.Unlock()
	return transportPressureSnapshot{name: "outbound_pending", queue: "outbound", depth: depth, bytes: bytes, bytesCapacity: bytesCapacity, result: "ok", revision: revision}, true
}

type transportPressureSnapshot struct {
	name                string
	queue               string
	depth               int
	capacity            int
	bytes               int64
	bytesCapacity       int64
	result              string
	revision            uint64
	publicationRevision uint64
}

type transportPressureSourceKey struct {
	source string
	name   string
	queue  string
}

type transportPressureGroupKey struct {
	name  string
	queue string
}

type transportPressureAggregator struct {
	mu                  sync.Mutex
	publicationRevision *atomic.Uint64
	bySource            map[transportPressureSourceKey]transportPressureSnapshot
	totals              map[transportPressureGroupKey]transportPressureSnapshot
}

var transportPressurePublicationRevision atomic.Uint64

func newTransportPressureAggregator() *transportPressureAggregator {
	return newTransportPressureAggregatorWithRevision(&transportPressurePublicationRevision)
}

func newTransportPressureAggregatorWithRevision(revision *atomic.Uint64) *transportPressureAggregator {
	if revision == nil {
		revision = &atomic.Uint64{}
	}
	return &transportPressureAggregator{
		publicationRevision: revision,
		bySource:            make(map[transportPressureSourceKey]transportPressureSnapshot),
		totals:              make(map[transportPressureGroupKey]transportPressureSnapshot),
	}
}

func (s *connState) observeTransportSnapshot(snapshot transportPressureSnapshot) {
	if snapshot.name == "" || s == nil || s.runtime == nil || s.runtime.opts.Observer == nil {
		return
	}
	s.pressureMu.Lock()
	defer s.pressureMu.Unlock()
	if s.pressureClosed {
		return
	}
	s.runtime.observeTransportPressure(connPressureSource(s.id), snapshot)
}

func (s *connState) observeTransport(name, queue string, depth, capacity int, bytes, bytesCapacity int64, result string) {
	if s == nil || s.runtime == nil || s.runtime.opts.Observer == nil {
		return
	}
	s.observeTransportSnapshot(transportPressureSnapshot{
		name:          name,
		queue:         queue,
		depth:         depth,
		capacity:      capacity,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		result:        result,
	})
}

func (s *connState) observeTransportForSource(source, name, queue string, depth, capacity int, bytes, bytesCapacity int64, result string) {
	s.observeTransportSnapshotForSource(source, transportPressureSnapshot{
		name:          name,
		queue:         queue,
		depth:         depth,
		capacity:      capacity,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		result:        result,
	})
}

func (s *connState) observeTransportSnapshotForSource(source string, snapshot transportPressureSnapshot) {
	if s == nil || s.runtime == nil {
		return
	}
	s.runtime.observeTransportPressure(source, snapshot)
}

func (r *listenerRuntime) observeTransportPressure(source string, snapshot transportPressureSnapshot) {
	if r == nil || r.opts.Observer == nil || snapshot.name == "" {
		return
	}
	if source == "" {
		source = "unknown"
	}
	pressure := r.transportPressureAggregator()
	event := pressure.apply(source, snapshot)
	r.emitTransportPressureSnapshot(event)
}

func (r *listenerRuntime) emitTransportPressureSnapshot(event transportPressureSnapshot) {
	if r == nil || r.opts.Observer == nil || event.name == "" {
		return
	}
	r.opts.Observer.OnTransportPressure(gatewaytypes.TransportPressureEvent{
		Name:          event.name,
		Queue:         event.queue,
		Depth:         event.depth,
		Capacity:      event.capacity,
		Bytes:         event.bytes,
		BytesCapacity: event.bytesCapacity,
		Result:        event.result,
		Revision:      event.publicationRevision,
	})
}

func (a *transportPressureAggregator) apply(source string, snapshot transportPressureSnapshot) transportPressureSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	sourceKey := transportPressureSourceKey{source: source, name: snapshot.name, queue: snapshot.queue}
	groupKey := transportPressureGroupKey{name: snapshot.name, queue: snapshot.queue}
	previous := a.bySource[sourceKey]
	next := previous
	next.name = snapshot.name
	next.queue = snapshot.queue
	gaugeUpdate := snapshot.result == "" || snapshot.result == "ok"
	acceptGauge := gaugeUpdate && ((snapshot.revision == 0 && previous.revision == 0) || snapshot.revision > previous.revision)
	if acceptGauge {
		next.depth = snapshot.depth
		next.capacity = snapshot.capacity
		next.bytes = snapshot.bytes
		next.bytesCapacity = snapshot.bytesCapacity
		next.revision = snapshot.revision
	} else if !gaugeUpdate {
		if snapshot.capacity > 0 || next.capacity == 0 {
			next.capacity = snapshot.capacity
		}
		if snapshot.bytesCapacity > 0 || next.bytesCapacity == 0 {
			next.bytesCapacity = snapshot.bytesCapacity
		}
	}

	total := a.totals[groupKey]
	total.name = snapshot.name
	total.queue = snapshot.queue
	total.depth += next.depth - previous.depth
	total.capacity += next.capacity - previous.capacity
	total.bytes += next.bytes - previous.bytes
	total.bytesCapacity += next.bytesCapacity - previous.bytesCapacity
	total = clampTransportPressureSnapshot(total)
	total.publicationRevision = a.publicationRevision.Add(1)
	a.bySource[sourceKey] = next
	a.totals[groupKey] = total
	total.result = snapshot.result
	return total
}

func (r *listenerRuntime) clearTransportPressureSource(source string) {
	if r == nil || source == "" {
		return
	}
	pressure := r.transportPressureAggregator()
	events := pressure.clear(source)
	for _, event := range events {
		r.emitTransportPressureSnapshot(event)
	}
}

func (a *transportPressureAggregator) clear(source string) []transportPressureSnapshot {
	var events []transportPressureSnapshot
	a.mu.Lock()
	for key, previous := range a.bySource {
		if key.source != source {
			continue
		}
		groupKey := transportPressureGroupKey{name: key.name, queue: key.queue}
		total := a.totals[groupKey]
		total.name = key.name
		total.queue = key.queue
		total.depth -= previous.depth
		total.capacity -= previous.capacity
		total.bytes -= previous.bytes
		total.bytesCapacity -= previous.bytesCapacity
		total = clampTransportPressureSnapshot(total)
		total.publicationRevision = a.publicationRevision.Add(1)
		a.totals[groupKey] = total
		delete(a.bySource, key)
		events = append(events, total)
	}
	a.mu.Unlock()
	return events
}

func (s *connState) closeTransportPressure() {
	if s == nil || s.runtime == nil {
		return
	}
	s.pressureMu.Lock()
	defer s.pressureMu.Unlock()
	if s.pressureClosed {
		return
	}
	s.pressureClosed = true
	s.runtime.clearTransportPressureSource(connPressureSource(s.id))
}

func (r *listenerRuntime) transportPressureAggregator() *transportPressureAggregator {
	if pressure := r.pressure.Load(); pressure != nil {
		return pressure
	}
	pressure := newTransportPressureAggregator()
	if r.pressure.CompareAndSwap(nil, pressure) {
		return pressure
	}
	return r.pressure.Load()
}

func clampTransportPressureSnapshot(snapshot transportPressureSnapshot) transportPressureSnapshot {
	if snapshot.depth < 0 {
		snapshot.depth = 0
	}
	if snapshot.capacity < 0 {
		snapshot.capacity = 0
	}
	if snapshot.bytes < 0 {
		snapshot.bytes = 0
	}
	if snapshot.bytesCapacity < 0 {
		snapshot.bytesCapacity = 0
	}
	return snapshot
}

func connPressureSource(id uint64) string {
	return "conn_" + strconv.FormatUint(id, 10)
}

func actorPressureSource(id int) string {
	if id < 0 {
		return "actor_unknown"
	}
	return "actor_" + strconv.Itoa(id)
}

func (s *connState) acquireOutboundWriteFrame() *wsWritevFrame {
	s.outboundMu.Lock()
	defer s.outboundMu.Unlock()

	if n := len(s.outboundWriteFrameFree); n > 0 {
		frame := s.outboundWriteFrameFree[n-1]
		s.outboundWriteFrameFree[n-1] = nil
		s.outboundWriteFrameFree = s.outboundWriteFrameFree[:n-1]
		return frame
	}
	return &wsWritevFrame{}
}

func (s *connState) releaseOutboundWriteFrame(frame *wsWritevFrame) {
	if frame == nil {
		return
	}

	frame.bufs[0] = nil
	frame.bufs[1] = nil

	s.outboundMu.Lock()
	s.outboundWriteFrameFree = append(s.outboundWriteFrameFree, frame)
	s.outboundMu.Unlock()
}

func releaseOutboundWriteCallback(conn gnetv2.Conn, err error) error {
	if conn == nil {
		return nil
	}
	if state, ok := conn.Context().(*connState); ok && state != nil {
		state.finishNextOutboundWrite(conn, err)
	}
	return nil
}

func (s *connState) abandonOutboundWrites() {
	if s == nil {
		return
	}
	s.outboundSubmitMu.Lock()
	defer s.outboundSubmitMu.Unlock()
	if s.outboundSubmitErr == nil {
		s.outboundSubmitErr = errOutboundSubmissionClosed
	}
	s.outboundMu.Lock()
	for i := range s.outboundWrites {
		// Do not reset or recycle an in-flight WebSocket frame: a gnet task may
		// still own its buffer slices after an ambiguous Trigger error.
		s.outboundWrites[i] = outboundWrite{}
	}
	s.outboundWrites = s.outboundWrites[:0]
	s.outboundPendingBytes = 0
	s.outboundBufferedBytes = 0
	s.outboundMu.Unlock()
}

func (s *connState) finishNextOutboundWrite(conn gnetv2.Conn, err error) {
	s.outboundMu.Lock()
	var write outboundWrite
	if conn != nil && len(s.outboundWrites) > 0 {
		write = s.outboundWrites[0]
		s.outboundWrites[0] = outboundWrite{}
		if len(s.outboundWrites) == 1 {
			s.outboundWrites = s.outboundWrites[:0]
		} else {
			s.outboundWrites = s.outboundWrites[1:]
		}
	}
	if s.maxOutboundBytes > 0 && write.size > 0 {
		s.outboundPendingBytes -= int64(write.size)
		if s.outboundPendingBytes < 0 {
			s.outboundPendingBytes = 0
		}
	}

	if conn != nil && err == nil {
		s.outboundBufferedBytes = int64(conn.OutboundBuffered())
	}
	s.outboundRevision++
	depth := len(s.outboundWrites)
	bytes := s.outboundPendingBytes + s.outboundBufferedBytes
	bytesCapacity := s.maxOutboundBytes
	snapshot := transportPressureSnapshot{
		name:          "outbound_pending",
		queue:         "outbound",
		depth:         depth,
		bytes:         bytes,
		bytesCapacity: bytesCapacity,
		revision:      s.outboundRevision,
	}
	s.outboundMu.Unlock()
	s.releaseOutboundWriteFrame(write.frame)
	if s.maxOutboundBytes > 0 {
		s.observeTransportSnapshot(snapshot)
	}
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
var _ transport.WebSocketMessageWriter = (*stateConn)(nil)
