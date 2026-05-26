package gnet

import (
	"bytes"
	"errors"
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

func TestStateConnWebSocketWritevRollbackPreservesEarlierInFlightWrite(t *testing.T) {
	raw := &errorAfterFirstWritevConn{err: errors.New("boom")}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 1 << 20,
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
	state.outboundMu.Lock()
	firstFrame := state.outboundWriteFrames[0]
	firstSize := state.outboundWriteSizes[0]
	state.outboundMu.Unlock()
	if firstFrame == nil || firstSize == 0 {
		t.Fatal("first websocket write was not tracked")
	}

	err := conn.WriteWebSocketMessage(payload, transport.WebSocketMessageText)
	if !errors.Is(err, raw.err) {
		t.Fatalf("second Write() error = %v, want %v", err, raw.err)
	}

	state.outboundMu.Lock()
	defer state.outboundMu.Unlock()
	if got, want := len(state.outboundWriteFrames), 1; got != want {
		t.Fatalf("in-flight write frames = %d, want %d", got, want)
	}
	if state.outboundWriteFrames[0] != firstFrame {
		t.Fatal("earlier in-flight write frame was replaced")
	}
	if got, want := len(state.outboundWriteSizes), 1; got != want {
		t.Fatalf("in-flight write sizes = %d, want %d", got, want)
	}
	if state.outboundWriteSizes[0] != firstSize {
		t.Fatalf("in-flight write size = %d, want %d", state.outboundWriteSizes[0], firstSize)
	}
	if got, want := len(state.outboundWriteFrameFree), 1; got != want {
		t.Fatalf("free frames = %d, want %d", got, want)
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

type errorAfterFirstWritevConn struct {
	allocTestGnetConn
	err         error
	writevCalls int
}

func (c *errorAfterFirstWritevConn) AsyncWritev(bs [][]byte, callback gnetv2.AsyncCallback) error {
	c.writevCalls++
	if c.writevCalls == 1 {
		c.lastAsyncWritev = bs
		c.lastAsyncWrite = nil
		return nil
	}
	return c.err
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
	raw := &allocTestGnetConn{autoAsyncCallback: true}
	state := &connState{
		raw:              raw,
		maxOutboundBytes: 4,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "tcp",
		}},
	}
	raw.ctx = state
	conn := &stateConn{state: state}

	if err := conn.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if err := conn.Write([]byte("1234")); err != nil {
		t.Fatalf("second Write() error = %v", err)
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
