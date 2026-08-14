package gnet

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestStateConnWebSocketWriteUsesCompactFrameForSmallPayload(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}}
	payload := []byte("hello websocket")

	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if raw.lastAsyncWrite == nil {
		t.Fatal("websocket Write used AsyncWritev, want compact AsyncWrite")
	}
	if raw.lastAsyncWritev != nil {
		t.Fatal("websocket Write recorded AsyncWritev, want compact AsyncWrite")
	}
	frame, _, err := decodeWSFrame(raw.lastAsyncWrite)
	if err != nil {
		t.Fatalf("decodeWSFrame(write): %v", err)
	}
	if frame.opcode != wsOpcodeText {
		t.Fatalf("opcode = %d, want text", frame.opcode)
	}
	if !bytes.Equal(frame.payload, payload) {
		t.Fatalf("payload = %q, want %q", frame.payload, payload)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	})
	if allocs != 1 {
		t.Fatalf("allocs = %.0f, want 1", allocs)
	}
}

func TestStateConnWebSocketWriteUsesWritevForLargePayload(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}}
	raw.ctx = conn.state
	payload := bytes.Repeat([]byte("x"), 1024)

	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if raw.lastAsyncWrite != nil {
		t.Fatal("websocket Write used AsyncWrite, want AsyncWritev")
	}
	if got, want := len(raw.lastAsyncWritev), 2; got != want {
		t.Fatalf("AsyncWritev buffers = %d, want %d", got, want)
	}
	if !bytes.Equal(raw.lastAsyncWritev[1], payload) {
		t.Fatalf("AsyncWritev payload = %q, want %q", raw.lastAsyncWritev[1], payload)
	}
	frame, _, err := decodeWSFrame(bytes.Join(raw.lastAsyncWritev, nil))
	if err != nil {
		t.Fatalf("decodeWSFrame(write): %v", err)
	}
	if frame.opcode != wsOpcodeText {
		t.Fatalf("opcode = %d, want text", frame.opcode)
	}
	if !bytes.Equal(frame.payload, payload) {
		t.Fatalf("payload length = %d, want %d", len(frame.payload), len(payload))
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	})
	if allocs != 1 {
		t.Fatalf("allocs = %.0f, want 1", allocs)
	}
}

func TestStateConnWebSocketWriteUsesNoAllocInSteadyStateForLargePayload(t *testing.T) {
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}}
	raw.ctx = conn.state
	payload := bytes.Repeat([]byte("x"), 1024)

	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("warmup Write() error = %v", err)
	}

	acquireAllocs := testing.AllocsPerRun(1000, func() {
		frame := conn.state.acquireOutboundWriteFrame()
		conn.state.releaseOutboundWriteFrame(frame)
	})
	if acquireAllocs != 0 {
		t.Fatalf("acquire/release allocs = %.0f, want 0", acquireAllocs)
	}

	buildAllocs := testing.AllocsPerRun(1000, func() {
		frame := conn.state.acquireOutboundWriteFrame()
		if err := buildWSWritevFrame(wsFrame{
			final:   true,
			opcode:  wsOpcodeText,
			payload: payload,
		}, frame); err != nil {
			conn.state.releaseOutboundWriteFrame(frame)
			panic(err)
		}
		conn.state.releaseOutboundWriteFrame(frame)
	})
	if buildAllocs != 0 {
		t.Fatalf("build allocs = %.0f, want 0", buildAllocs)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("write allocs = %.0f, want 0 in steady state", allocs)
	}
}

func TestStateConnWebSocketWritevAllocBreakdown(t *testing.T) {
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	payload := bytes.Repeat([]byte("x"), 1024)

	// Warm up the per-connection frame cache and outbound queues.
	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("warmup Write() error = %v", err)
	}

	frameAllocs := testing.AllocsPerRun(1000, func() {
		framed := state.acquireOutboundWriteFrame()
		if err := buildWSWritevFrame(wsFrame{
			final:   true,
			opcode:  wsOpcodeText,
			payload: payload,
		}, framed); err != nil {
			state.releaseOutboundWriteFrame(framed)
			panic(err)
		}
		state.releaseOutboundWriteFrame(framed)
	})
	if frameAllocs != 0 {
		t.Fatalf("frame allocs = %.0f, want 0", frameAllocs)
	}

	writevNoCallbackAllocs := testing.AllocsPerRun(1000, func() {
		framed := state.acquireOutboundWriteFrame()
		if err := buildWSWritevFrame(wsFrame{
			final:   true,
			opcode:  wsOpcodeText,
			payload: payload,
		}, framed); err != nil {
			state.releaseOutboundWriteFrame(framed)
			panic(err)
		}
		if err := raw.AsyncWritev(framed.bufs[:], nil); err != nil {
			state.releaseOutboundWriteFrame(framed)
			panic(err)
		}
		state.releaseOutboundWriteFrame(framed)
	})
	if writevNoCallbackAllocs != 0 {
		t.Fatalf("writev without callback allocs = %.0f, want 0", writevNoCallbackAllocs)
	}

	writevCallbackAllocs := testing.AllocsPerRun(1000, func() {
		framed := state.acquireOutboundWriteFrame()
		if err := buildWSWritevFrame(wsFrame{
			final:   true,
			opcode:  wsOpcodeText,
			payload: payload,
		}, framed); err != nil {
			state.releaseOutboundWriteFrame(framed)
			panic(err)
		}
		if err := raw.AsyncWritev(framed.bufs[:], releaseOutboundWriteCallback); err != nil {
			state.releaseOutboundWriteFrame(framed)
			panic(err)
		}
		state.releaseOutboundWriteFrame(framed)
	})
	if writevCallbackAllocs != 0 {
		t.Fatalf("writev with callback allocs = %.0f, want 0", writevCallbackAllocs)
	}
}

func TestStateConnWebSocketWriteReusesWritevFrame(t *testing.T) {
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	payload := bytes.Repeat([]byte("x"), 1024)

	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free frames after first write = %d, want 1", got)
	}
	first := state.outboundWriteFrameFree[0]

	if err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free frames after second write = %d, want 1", got)
	}
	if state.outboundWriteFrameFree[0] != first {
		t.Fatal("writev frame was not reused")
	}
}

func TestStateConnWebSocketWritevTriggerErrorRetainsCallbackOwnership(t *testing.T) {
	errTrigger := errors.New("trigger failed after enqueue")
	raw := &queuedAsyncWriteConn{writevErrors: []error{errTrigger}}
	observer := &recordingTransportPressureObserver{}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network:  "websocket",
			Observer: observer,
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	payload := bytes.Repeat([]byte("x"), 1024)

	err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText)
	if !errors.Is(err, errTrigger) {
		t.Fatalf("Write() error = %v, want %v", err, errTrigger)
	}

	state.outboundMu.Lock()
	if got, want := len(state.outboundWrites), 1; got != want {
		t.Fatalf("in-flight writes = %d, want %d", got, want)
	}
	if state.outboundWrites[0].frame == nil || state.outboundWrites[0].size == 0 {
		t.Fatalf("in-flight vector write = %+v, want frame and byte reservation", state.outboundWrites[0])
	}
	if got := len(state.outboundWriteFrameFree); got != 0 {
		t.Fatalf("free frames before callback = %d, want 0", got)
	}
	state.outboundMu.Unlock()
	if got := len(raw.writevData); got != 1 {
		t.Fatalf("queued writev payloads = %d, want 1", got)
	}
	if _, _, err := decodeWSFrame(bytes.Join(raw.writevData[0], nil)); err != nil {
		t.Fatalf("queued websocket frame was mutated after trigger error: %v", err)
	}
	raw.completeWritev(t, 0)
	state.outboundMu.Lock()
	if len(state.outboundWrites) != 0 {
		t.Fatalf("callback retained %d outbound writes", len(state.outboundWrites))
	}
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free frames after callback = %d, want 1", got)
	}
	state.outboundMu.Unlock()
	events := observer.snapshot()
	if last := events[len(events)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last websocket pressure after callback = %+v, want zero", last)
	}
}

func TestStateConnWebSocketCompactCallbackDoesNotReleaseFollowingVector(t *testing.T) {
	raw := &queuedAsyncWriteConn{}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	vectorPayload := bytes.Repeat([]byte("v"), wsWritevPayloadThreshold)

	if err := conn.WriteWebSocketMessage([]byte("compact"), transport.WebSocketMessageText); err != nil {
		t.Fatalf("compact WriteWebSocketMessage() error = %v", err)
	}
	if err := conn.WriteWebSocketMessage(vectorPayload, transport.WebSocketMessageBinary); err != nil {
		t.Fatalf("vector WriteWebSocketMessage() error = %v", err)
	}

	raw.completeWrite(t, 0)
	frame, _, err := decodeWSFrame(bytes.Join(raw.writevData[0], nil))
	if err != nil {
		t.Fatalf("vector frame after compact callback: %v", err)
	}
	if !bytes.Equal(frame.payload, vectorPayload) {
		t.Fatalf("vector payload after compact callback has length %d, want %d", len(frame.payload), len(vectorPayload))
	}
	state.outboundMu.Lock()
	if got := len(state.outboundWriteFrameFree); got != 0 {
		state.outboundMu.Unlock()
		t.Fatalf("free vector frames before vector callback = %d, want 0", got)
	}
	state.outboundMu.Unlock()

	raw.completeWritev(t, 0)
	state.outboundMu.Lock()
	defer state.outboundMu.Unlock()
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free vector frames after vector callback = %d, want 1", got)
	}
}

func TestStateConnWebSocketCompactCallbackDoesNotReleaseReusedFollowingVector(t *testing.T) {
	raw := &queuedAsyncWriteConn{}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	firstVector := bytes.Repeat([]byte("a"), wsWritevPayloadThreshold)
	secondVector := bytes.Repeat([]byte("b"), wsWritevPayloadThreshold)

	if err := conn.WriteWebSocketMessage(firstVector, transport.WebSocketMessageBinary); err != nil {
		t.Fatalf("first vector WriteWebSocketMessage() error = %v", err)
	}
	if err := conn.WriteWebSocketMessage([]byte("compact"), transport.WebSocketMessageText); err != nil {
		t.Fatalf("compact WriteWebSocketMessage() error = %v", err)
	}
	raw.completeWritev(t, 0)
	if err := conn.WriteWebSocketMessage(secondVector, transport.WebSocketMessageBinary); err != nil {
		t.Fatalf("second vector WriteWebSocketMessage() error = %v", err)
	}

	raw.completeWrite(t, 0)
	frame, _, err := decodeWSFrame(bytes.Join(raw.writevData[1], nil))
	if err != nil {
		t.Fatalf("second vector frame after compact callback: %v", err)
	}
	if !bytes.Equal(frame.payload, secondVector) {
		t.Fatalf("second vector payload after compact callback has length %d, want %d", len(frame.payload), len(secondVector))
	}
	state.outboundMu.Lock()
	if got := len(state.outboundWriteFrameFree); got != 0 {
		state.outboundMu.Unlock()
		t.Fatalf("free vector frames before second vector callback = %d, want 0", got)
	}
	state.outboundMu.Unlock()

	raw.completeWritev(t, 1)
	state.outboundMu.Lock()
	defer state.outboundMu.Unlock()
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free vector frames after second vector callback = %d, want 1", got)
	}
}

func TestStateConnWebSocketTriggerErrorKeepsFollowingVectorUntilLateCallback(t *testing.T) {
	errTrigger := errors.New("trigger failed after enqueue")
	raw := &queuedAsyncWriteConn{writevErrors: []error{errTrigger}}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	vectorPayload := bytes.Repeat([]byte("e"), wsWritevPayloadThreshold)

	if err := conn.WriteWebSocketMessage([]byte("compact"), transport.WebSocketMessageText); err != nil {
		t.Fatalf("compact WriteWebSocketMessage() error = %v", err)
	}
	if err := conn.WriteWebSocketMessage(vectorPayload, transport.WebSocketMessageBinary); !errors.Is(err, errTrigger) {
		t.Fatalf("vector WriteWebSocketMessage() error = %v, want %v", err, errTrigger)
	}

	raw.completeWrite(t, 0)
	frame, _, err := decodeWSFrame(bytes.Join(raw.writevData[0], nil))
	if err != nil {
		t.Fatalf("error-owned vector frame after compact callback: %v", err)
	}
	if !bytes.Equal(frame.payload, vectorPayload) {
		t.Fatalf("error-owned vector payload after compact callback has length %d, want %d", len(frame.payload), len(vectorPayload))
	}
	state.outboundMu.Lock()
	if got := len(state.outboundWriteFrameFree); got != 0 {
		state.outboundMu.Unlock()
		t.Fatalf("free vector frames before late error callback = %d, want 0", got)
	}
	state.outboundMu.Unlock()

	raw.completeWritev(t, 0)
	state.outboundMu.Lock()
	defer state.outboundMu.Unlock()
	if got := len(state.outboundWriteFrameFree); got != 1 {
		t.Fatalf("free vector frames after late error callback = %d, want 1", got)
	}
}

func TestConnStateCloseDoesNotRecycleCallbackOwnedVectorFrame(t *testing.T) {
	observer := &recordingTransportPressureObserver{}
	raw := &queuedAsyncWriteConn{}
	state := &connState{
		id:               91,
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network:  "websocket",
			Observer: observer,
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	vectorPayload := bytes.Repeat([]byte("c"), wsWritevPayloadThreshold)

	if err := conn.WriteWebSocketMessage([]byte("compact"), transport.WebSocketMessageText); err != nil {
		t.Fatalf("compact WriteWebSocketMessage() error = %v", err)
	}
	if err := conn.WriteWebSocketMessage(vectorPayload, transport.WebSocketMessageBinary); err != nil {
		t.Fatalf("vector WriteWebSocketMessage() error = %v", err)
	}
	if done := state.handleEvent(connEvent{kind: connEventClose}); !done {
		t.Fatal("close event did not terminate connection processing")
	}

	frame, _, err := decodeWSFrame(bytes.Join(raw.writevData[0], nil))
	if err != nil {
		t.Fatalf("callback-owned vector frame after close: %v", err)
	}
	if !bytes.Equal(frame.payload, vectorPayload) {
		t.Fatalf("callback-owned vector payload after close has length %d, want %d", len(frame.payload), len(vectorPayload))
	}
	raw.completeWrite(t, 0)
	raw.completeWritev(t, 0)
	state.outboundMu.Lock()
	if got := len(state.outboundWrites); got != 0 {
		state.outboundMu.Unlock()
		t.Fatalf("outbound writes after close callbacks = %d, want 0", got)
	}
	if got := len(state.outboundWriteFrameFree); got != 0 {
		state.outboundMu.Unlock()
		t.Fatalf("recycled callback-owned vector frames after close = %d, want 0", got)
	}
	state.outboundMu.Unlock()
	events := observer.snapshot()
	if last := events[len(events)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last pressure after close callbacks = %+v, want cleared zero", last)
	}
}

func TestStateConnWebSocketVectorWriteDirectAllocBreakdown(t *testing.T) {
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}
	payload := bytes.Repeat([]byte("x"), 1024)

	if err := conn.writeWebSocketVector(payload, transport.WebSocketMessageText); err != nil {
		t.Fatalf("warmup writeWebSocketVector error = %v", err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.writeWebSocketVector(payload, transport.WebSocketMessageText); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("writeWebSocketVector allocs = %.0f, want 0", allocs)
	}
}

type queuedAsyncWriteConn struct {
	allocTestGnetConn
	writeErrors     []error
	writevErrors    []error
	writeCallbacks  []gnetv2.AsyncCallback
	writevCallbacks []gnetv2.AsyncCallback
	writevData      [][][]byte
}

func (c *queuedAsyncWriteConn) AsyncWrite(_ []byte, callback gnetv2.AsyncCallback) error {
	index := len(c.writeCallbacks)
	c.writeCallbacks = append(c.writeCallbacks, callback)
	if index < len(c.writeErrors) {
		return c.writeErrors[index]
	}
	return nil
}

func (c *queuedAsyncWriteConn) AsyncWritev(data [][]byte, callback gnetv2.AsyncCallback) error {
	index := len(c.writevCallbacks)
	c.writevCallbacks = append(c.writevCallbacks, callback)
	c.writevData = append(c.writevData, data)
	if index < len(c.writevErrors) {
		return c.writevErrors[index]
	}
	return nil
}

func (c *queuedAsyncWriteConn) completeWrite(t *testing.T, index int) {
	t.Helper()
	if index < 0 || index >= len(c.writeCallbacks) || c.writeCallbacks[index] == nil {
		t.Fatalf("write callback %d is unavailable", index)
	}
	if err := c.writeCallbacks[index](c, nil); err != nil {
		t.Fatalf("write callback %d: %v", index, err)
	}
}

func (c *queuedAsyncWriteConn) completeWritev(t *testing.T, index int) {
	t.Helper()
	if index < 0 || index >= len(c.writevCallbacks) || c.writevCallbacks[index] == nil {
		t.Fatalf("writev callback %d is unavailable", index)
	}
	if err := c.writevCallbacks[index](c, nil); err != nil {
		t.Fatalf("writev callback %d: %v", index, err)
	}
}

func TestStateConnWebSocketWriteDefaultsBinaryWithoutHint(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}}

	if err := conn.Write([]byte("valid utf8 text")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	frame, _, err := decodeWSFrame(raw.lastAsyncWrite)
	if err != nil {
		t.Fatalf("decodeWSFrame(write): %v", err)
	}
	if frame.opcode != wsOpcodeBinary {
		t.Fatalf("opcode = %d, want binary without protocol hint", frame.opcode)
	}
}

func TestStateConnTCPWriteDoesNotAllocatePayloadCopy(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "tcp",
		}},
	}}
	payload := []byte("hello tcp")

	if err := conn.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !bytes.Equal(raw.lastAsyncWrite, payload) {
		t.Fatalf("payload = %q, want %q", raw.lastAsyncWrite, payload)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.Write(payload); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("allocs = %.0f, want 0", allocs)
	}
}

func TestStateConnRejectsWriteOverOutboundByteLimit(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw:              raw,
		maxOutboundBytes: 4,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "tcp",
		}},
	}}

	err := conn.Write([]byte("12345"))
	if !errors.Is(err, transport.ErrOutboundBytesExceeded) {
		t.Fatalf("Write() error = %v, want %v", err, transport.ErrOutboundBytesExceeded)
	}
	if raw.lastAsyncWrite != nil {
		t.Fatal("oversized write reached gnet AsyncWrite")
	}
}

func TestStateConnReleasesOutboundReservationAfterAsyncCallback(t *testing.T) {
	observer := &recordingTransportPressureObserver{}
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 4,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network:  "tcp",
			Observer: observer,
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}

	if err := conn.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	firstEvents := observer.snapshot()
	if last := firstEvents[len(firstEvents)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last pressure after inline callback = %+v, want released zero", last)
	}
	if err := conn.Write([]byte("1234")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	secondEvents := observer.snapshot()
	if last := secondEvents[len(secondEvents)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last pressure after second inline callback = %+v, want released zero", last)
	}
}

func TestStateConnAsyncWriteTriggerErrorRetainsFIFOCallbackOwnership(t *testing.T) {
	errTrigger := errors.New("trigger failed after enqueue")
	raw := &queuedAsyncWriteConn{writeErrors: []error{nil, errTrigger}}
	observer := &recordingTransportPressureObserver{}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 16,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network:  "tcp",
			Observer: observer,
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}

	if err := conn.Write([]byte("ab")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if err := conn.Write([]byte("wxyz")); !errors.Is(err, errTrigger) {
		t.Fatalf("second Write() error = %v, want %v", err, errTrigger)
	}
	state.outboundMu.Lock()
	if got, want := outboundWriteSizesLocked(state), []int{2, 4}; !slices.Equal(got, want) {
		state.outboundMu.Unlock()
		t.Fatalf("reservations after trigger error = %v, want %v", got, want)
	}
	state.outboundMu.Unlock()
	if err := conn.Write([]byte("late")); !errors.Is(err, errTrigger) {
		t.Fatalf("write after ambiguous trigger error = %v, want original failure", err)
	}
	if got := len(raw.writeCallbacks); got != 2 {
		t.Fatalf("raw AsyncWrite calls = %d, want no submission after failure", got)
	}

	raw.completeWrite(t, 0)
	state.outboundMu.Lock()
	if got, want := outboundWriteSizesLocked(state), []int{4}; !slices.Equal(got, want) {
		state.outboundMu.Unlock()
		t.Fatalf("reservations after first callback = %v, want %v", got, want)
	}
	state.outboundMu.Unlock()
	raw.completeWrite(t, 1)
	events := observer.snapshot()
	if last := events[len(events)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last pressure after queued error callback = %+v, want zero", last)
	}
}

func outboundWriteSizesLocked(state *connState) []int {
	sizes := make([]int, len(state.outboundWrites))
	for i := range state.outboundWrites {
		sizes[i] = state.outboundWrites[i].size
	}
	return sizes
}

func TestStateConnWebSocketReleasesOutboundReservationAfterInlineCallback(t *testing.T) {
	observer := &recordingTransportPressureObserver{}
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network:  "websocket",
			Observer: observer,
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}

	if err := conn.WriteWebSocketMessage(bytes.Repeat([]byte("x"), 1024), transport.WebSocketMessageBinary); err != nil {
		t.Fatalf("WriteWebSocketMessage() error = %v", err)
	}
	events := observer.snapshot()
	if len(events) == 0 {
		t.Fatal("websocket write published no outbound pressure observations")
	}
	if last := events[len(events)-1]; last.Depth != 0 || last.Bytes != 0 {
		t.Fatalf("last websocket pressure after inline callback = %+v, want released zero", last)
	}
}

func TestStateConnOutboundLimitIncludesGnetBufferedBytes(t *testing.T) {
	raw := &allocTestGnetConn{autoAsyncCallback: true, outboundBuffered: 4}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 8,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "tcp",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}

	if err := conn.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	err := conn.Write([]byte("12345"))
	if !errors.Is(err, transport.ErrOutboundBytesExceeded) {
		t.Fatalf("second Write() error = %v, want %v", err, transport.ErrOutboundBytesExceeded)
	}
}
