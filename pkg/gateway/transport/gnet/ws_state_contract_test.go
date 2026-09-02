package gnet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestConnStateProcessesWebSocketControlAndCloseFrames(t *testing.T) {
	state := &connState{mode: connModeWSFrames}
	state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodePing, [4]byte{1, 2, 3, 4}, []byte("health"))
	result := requireWSResult(t, state)
	if result.closeNow || len(result.write) == 0 {
		t.Fatalf("ping result = %+v, want pong write", result)
	}
	pong, _, err := decodeWSFrame(result.write)
	if err != nil || pong.opcode != wsOpcodePong || !bytes.Equal(pong.payload, []byte("health")) {
		t.Fatalf("pong frame = %+v, %v", pong, err)
	}

	pongInput := encodeMaskedTestWSFrame(t, true, wsOpcodePong, [4]byte{5, 6, 7, 8}, []byte("ignored"))
	textInput := encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{9, 10, 11, 12}, []byte("next"))
	state.wsInbound = append(pongInput, textInput...)
	result = requireWSResult(t, state)
	if result.opcode != wsOpcodeText || !bytes.Equal(result.payload, []byte("next")) {
		t.Fatalf("pong followed by text = %+v", result)
	}

	state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeClose, [4]byte{1, 1, 1, 1}, nil)
	result = requireWSResult(t, state)
	if !result.closeNow || result.closeErr != nil || len(result.closeWrite) == 0 {
		t.Fatalf("empty close result = %+v", result)
	}
	closeFrame, _, err := decodeWSFrame(result.closeWrite)
	if err != nil {
		t.Fatalf("decode close response: %v", err)
	}
	if got := binary.BigEndian.Uint16(closeFrame.payload); got != wsCloseNormalClosure {
		t.Fatalf("empty close response code = %d, want %d", got, wsCloseNormalClosure)
	}

	state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeClose, [4]byte{2, 2, 2, 2}, nil)
	result = requireWSResult(t, state)
	if !result.closeNow || len(result.closeWrite) != 0 {
		t.Fatalf("repeated close result = %+v, want close without duplicate write", result)
	}

	payload := []byte{byte(wsCloseNormalClosure >> 8), byte(wsCloseNormalClosure & 0xff), 'b', 'y', 'e'}
	echoState := &connState{mode: connModeWSFrames}
	echoState.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeClose, [4]byte{3, 3, 3, 3}, payload)
	result = requireWSResult(t, echoState)
	echo, _, err := decodeWSFrame(result.closeWrite)
	if err != nil || !bytes.Equal(echo.payload, payload) {
		t.Fatalf("close echo = %+v, %v, want payload %v", echo, err, payload)
	}
}

func TestConnStateRejectsInvalidWebSocketMessageTransitions(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*connState)
		wantCode  uint16
	}{
		{
			name: "unmasked client frame",
			configure: func(state *connState) {
				frame, err := encodeWSFrame(wsFrame{final: true, opcode: wsOpcodeBinary, payload: []byte("data")})
				if err != nil {
					t.Fatalf("encode unmasked frame: %v", err)
				}
				state.wsInbound = frame
			},
			wantCode: wsCloseProtocolError,
		},
		{
			name: "unexpected continuation",
			configure: func(state *connState) {
				state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeContinuation, [4]byte{1, 2, 3, 4}, []byte("tail"))
			},
			wantCode: wsCloseProtocolError,
		},
		{
			name: "new message before continuation",
			configure: func(state *connState) {
				state.wsOpcode = wsOpcodeBinary
				state.wsFragment = []byte("head")
				state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte("other"))
			},
			wantCode: wsCloseProtocolError,
		},
		{
			name: "invalid text",
			configure: func(state *connState) {
				state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte{0xff})
			},
			wantCode: wsCloseInvalidData,
		},
		{
			name: "invalid fragmented text",
			configure: func(state *connState) {
				state.wsOpcode = wsOpcodeText
				state.wsFragment = []byte{'a'}
				state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeContinuation, [4]byte{1, 2, 3, 4}, []byte{0xff})
			},
			wantCode: wsCloseInvalidData,
		},
		{
			name: "invalid close payload",
			configure: func(state *connState) {
				state.wsInbound = encodeMaskedTestWSFrame(t, true, wsOpcodeClose, [4]byte{1, 2, 3, 4}, []byte{1})
			},
			wantCode: wsCloseProtocolError,
		},
		{
			name: "unsupported opcode",
			configure: func(state *connState) {
				state.wsInbound = encodeMaskedTestWSFrame(t, true, 0x3, [4]byte{1, 2, 3, 4}, []byte("data"))
			},
			wantCode: wsCloseProtocolError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &connState{mode: connModeWSFrames}
			tt.configure(state)
			result := requireWSResult(t, state)
			if !result.closeNow || result.closeErr == nil || len(result.closeWrite) == 0 {
				t.Fatalf("result = %+v, want protocol close", result)
			}
			if got := wsCloseCodeForErr(result.closeErr); got != tt.wantCode {
				t.Fatalf("close error code = %d, want %d", got, tt.wantCode)
			}
			frame, _, err := decodeWSFrame(result.closeWrite)
			if err != nil {
				t.Fatalf("decode close write: %v", err)
			}
			if got := binary.BigEndian.Uint16(frame.payload); got != tt.wantCode {
				t.Fatalf("wire close code = %d, want %d", got, tt.wantCode)
			}
		})
	}
}

func TestConnStateReassemblesWebSocketFragmentsAndWaitsForCompletion(t *testing.T) {
	state := &connState{mode: connModeWSFrames}
	if result, ok := state.nextWSResult(); ok {
		t.Fatalf("empty input result = %+v, want pending", result)
	}

	first := encodeMaskedTestWSFrame(t, false, wsOpcodeBinary, [4]byte{1, 2, 3, 4}, []byte("one"))
	middle := encodeMaskedTestWSFrame(t, false, wsOpcodeContinuation, [4]byte{5, 6, 7, 8}, []byte("-two"))
	last := encodeMaskedTestWSFrame(t, true, wsOpcodeContinuation, [4]byte{9, 10, 11, 12}, []byte("-three"))
	state.wsInbound = append(append(first, middle...), last...)
	result := requireWSResult(t, state)
	if result.opcode != wsOpcodeBinary || !bytes.Equal(result.payload, []byte("one-two-three")) {
		t.Fatalf("fragment result = %+v", result)
	}
	if state.wsOpcode != 0 || state.wsFragment != nil || len(state.wsInbound) != 0 {
		t.Fatalf("fragment state retained opcode=%d fragment=%q inbound=%d", state.wsOpcode, state.wsFragment, len(state.wsInbound))
	}
}

func TestConnEventLifecycleOrdersFailureBeforeClose(t *testing.T) {
	openErr := errors.New("open rejected")
	handler := &recordingConnContractHandler{openErr: openErr}
	raw := &contractGnetConn{}
	runtime := &listenerRuntime{
		opts:    transport.ListenerOptions{Network: "tcp"},
		handler: handler,
		active:  true,
	}
	state := newConnState(7, raw, runtime)
	state.enqueueOpen()
	state.processReady()

	if got, want := handler.events, []string{"open", "close"}; !equalConnContractEvents(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if !errors.Is(handler.closeErr, openErr) {
		t.Fatalf("close error = %v, want %v", handler.closeErr, openErr)
	}
	if raw.closeCalls != 1 {
		t.Fatalf("raw close calls = %d, want 1", raw.closeCalls)
	}
	if !state.closing || state.scheduled.Load() {
		t.Fatalf("final state closing=%v scheduled=%v", state.closing, state.scheduled.Load())
	}
}

func TestConnEventLifecycleRecordsOpcodeAndClosesAfterDataFailure(t *testing.T) {
	dataErr := errors.New("handler failed")
	handler := &recordingConnContractHandler{dataErr: dataErr}
	raw := &contractGnetConn{}
	runtime := &listenerRuntime{
		opts:    transport.ListenerOptions{Network: "websocket"},
		handler: handler,
		active:  true,
	}
	state := newConnState(8, raw, runtime)
	state.notifyClose = true
	if !state.enqueueDataWithOpcode(wsOpcodeText, []byte("payload")) {
		t.Fatal("enqueueDataWithOpcode rejected payload")
	}
	state.processReady()

	if got, want := handler.events, []string{"data:payload", "close"}; !equalConnContractEvents(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if got := byte(state.wsWriteOp.Load()); got != wsOpcodeText {
		t.Fatalf("write opcode = %d, want text", got)
	}
	if state.pendingBytes != 0 || raw.closeCalls != 1 || !errors.Is(handler.closeErr, dataErr) {
		t.Fatalf("final pending=%d closes=%d closeErr=%v", state.pendingBytes, raw.closeCalls, handler.closeErr)
	}
}

func TestStateConnCloseUsesWebSocketCloseHandshakeAndFallsBack(t *testing.T) {
	raw := &contractGnetConn{autoCallback: true}
	state := &connState{
		raw:     raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{Network: "websocket"}},
	}
	conn := &stateConn{state: state}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if raw.closeCalls != 1 || len(raw.lastAsyncWrite) == 0 {
		t.Fatalf("websocket close writes=%d raw closes=%d", len(raw.lastAsyncWrite), raw.closeCalls)
	}
	frame, _, err := decodeWSFrame(raw.lastAsyncWrite)
	if err != nil || frame.opcode != wsOpcodeClose {
		t.Fatalf("close frame = %+v, %v", frame, err)
	}

	writeErr := errors.New("write unavailable")
	fallbackRaw := &contractGnetConn{asyncWriteErr: writeErr}
	fallback := &stateConn{state: &connState{
		raw:     fallbackRaw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{Network: "websocket"}},
	}}
	if err := fallback.Close(); err != nil {
		t.Fatalf("fallback Close(): %v", err)
	}
	if fallbackRaw.closeCalls != 1 {
		t.Fatalf("fallback raw close calls = %d, want 1", fallbackRaw.closeCalls)
	}
	if err := fallback.Close(); err != nil {
		t.Fatalf("idempotent fallback Close(): %v", err)
	}
	if fallbackRaw.closeCalls != 2 {
		t.Fatalf("second raw close calls = %d, want 2", fallbackRaw.closeCalls)
	}

	tcpRaw := &contractGnetConn{}
	tcp := &stateConn{state: &connState{raw: tcpRaw}}
	if err := tcp.Close(); err != nil || tcpRaw.closeCalls != 1 {
		t.Fatalf("TCP Close() = %v, raw closes=%d", err, tcpRaw.closeCalls)
	}
}

func requireWSResult(t *testing.T, state *connState) wsTrafficResult {
	t.Helper()
	result, ok := state.nextWSResult()
	if !ok {
		t.Fatal("nextWSResult() returned pending")
	}
	return result
}

type recordingConnContractHandler struct {
	openErr  error
	dataErr  error
	closeErr error
	events   []string
}

func (h *recordingConnContractHandler) OnOpen(transport.Conn) error {
	h.events = append(h.events, "open")
	return h.openErr
}

func (h *recordingConnContractHandler) OnData(_ transport.Conn, data []byte) error {
	h.events = append(h.events, "data:"+string(data))
	return h.dataErr
}

func (h *recordingConnContractHandler) OnClose(_ transport.Conn, err error) {
	h.events = append(h.events, "close")
	h.closeErr = err
}

type contractGnetConn struct {
	allocTestGnetConn
	nextErr       error
	asyncWriteErr error
	closeErr      error
	closeCalls    int
	autoCallback  bool
}

func (c *contractGnetConn) Next(n int) ([]byte, error) {
	if c.nextErr != nil {
		return nil, c.nextErr
	}
	return c.allocTestGnetConn.Next(n)
}

func (c *contractGnetConn) AsyncWrite(buf []byte, callback gnetv2.AsyncCallback) error {
	c.lastAsyncWrite = append(c.lastAsyncWrite[:0], buf...)
	if c.asyncWriteErr != nil {
		return c.asyncWriteErr
	}
	if c.autoCallback && callback != nil {
		return callback(c, nil)
	}
	return nil
}

func (c *contractGnetConn) Close() error {
	c.closeCalls++
	return c.closeErr
}

func equalConnContractEvents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
