package core

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var benchmarkEncodeWriteErr error

func TestServerWriteFrameUsesTransportAsyncWriteDirectly(t *testing.T) {
	encoded := []byte("encoded-payload")
	conn := &recordingWriteConn{}
	srv := &Server{}
	state := &sessionState{
		server:   srv,
		conn:     conn,
		listener: &listenerRuntime{adapter: staticEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}
	state.session = session.New(session.Config{
		ID: 1,
		WriteFrameFn: func(f frame.Frame, meta session.OutboundMeta) error {
			return srv.encodeAndWrite(state, f, meta)
		},
	})

	if err := state.session.WriteFrame(&frame.PingPacket{}); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	if conn.writeCalls != 1 {
		t.Fatalf("write calls = %d, want 1", conn.writeCalls)
	}
	if !bytes.Equal(conn.writePayload, encoded) {
		t.Fatalf("write payload = %q, want %q", conn.writePayload, encoded)
	}
}

func TestServerEncodeAndWriteMapsTransportOutboundOverflow(t *testing.T) {
	conn := &recordingWriteConn{writeErr: transport.ErrOutboundBytesExceeded}
	srv := &Server{}
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  session.New(session.Config{ID: 2}),
		listener: &listenerRuntime{adapter: staticEncodeAdapter{encoded: []byte("too-large")}},
		closedCh: make(chan struct{}),
	}

	err := srv.encodeAndWrite(state, &frame.PingPacket{}, session.OutboundMeta{})
	if !errors.Is(err, transport.ErrOutboundBytesExceeded) {
		t.Fatalf("encodeAndWrite() error = %v, want %v", err, transport.ErrOutboundBytesExceeded)
	}
	if got := closeReasonForError(err, gatewaytypes.CloseReasonPeerClosed); got != gatewaytypes.CloseReasonOutboundOverflow {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonOutboundOverflow)
	}
}

func TestServerWritePayloadDirectUsesWebSocketProtocolMessageType(t *testing.T) {
	tests := []struct {
		name         string
		protocol     string
		selected     string
		wantType     transport.WebSocketMessageType
		wantFallback bool
	}{
		{name: "wkproto", protocol: "wkproto", wantType: transport.WebSocketMessageBinary},
		{name: "jsonrpc", protocol: "jsonrpc", wantType: transport.WebSocketMessageText},
		{name: "wsmux selected wkproto", protocol: "wsmux", selected: "wkproto", wantType: transport.WebSocketMessageBinary},
		{name: "wsmux selected jsonrpc", protocol: "wsmux", selected: "jsonrpc", wantType: transport.WebSocketMessageText},
		{name: "wsmux unselected falls back", protocol: "wsmux", wantFallback: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &webSocketMessageTypeConn{}
			sess := session.New(session.Config{ID: 1})
			if tt.selected != "" {
				sess.SetValue(gatewaytypes.SessionValueProtocolName, tt.selected)
			}
			srv := &Server{}
			state := &sessionState{
				conn:    conn,
				session: sess,
				listener: &listenerRuntime{options: gatewaytypes.ListenerOptions{
					Network:  "websocket",
					Protocol: tt.protocol,
				}},
				closedCh: make(chan struct{}),
			}

			if err := srv.writePayloadDirect(state, []byte("payload")); err != nil {
				t.Fatalf("writePayloadDirect() error = %v", err)
			}
			if tt.wantFallback {
				if conn.writeCalls != 1 || conn.messageCalls != 0 {
					t.Fatalf("calls = write:%d message:%d, want fallback Write", conn.writeCalls, conn.messageCalls)
				}
				return
			}
			if conn.messageCalls != 1 || conn.writeCalls != 0 {
				t.Fatalf("calls = write:%d message:%d, want WriteWebSocketMessage", conn.writeCalls, conn.messageCalls)
			}
			if conn.messageType != tt.wantType {
				t.Fatalf("message type = %v, want %v", conn.messageType, tt.wantType)
			}
		})
	}
}

type staticEncodeAdapter struct {
	encoded []byte
}

func (a staticEncodeAdapter) Name() string { return "static-encode" }

func (a staticEncodeAdapter) Decode(session.Session, []byte) ([]frame.Frame, int, error) {
	return nil, 0, nil
}

func (a staticEncodeAdapter) Encode(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error) {
	return a.encoded, nil
}

func (a staticEncodeAdapter) OnOpen(session.Session) error  { return nil }
func (a staticEncodeAdapter) OnClose(session.Session) error { return nil }

type recordingWriteConn struct {
	writeCalls   int
	messageCalls int
	writePayload []byte
	writeErr     error
}

func (c *recordingWriteConn) ID() uint64         { return 4 }
func (c *recordingWriteConn) Close() error       { return nil }
func (c *recordingWriteConn) LocalAddr() string  { return "local" }
func (c *recordingWriteConn) RemoteAddr() string { return "remote" }

func (c *recordingWriteConn) Write(payload []byte) error {
	c.writeCalls++
	c.writePayload = payload
	return c.writeErr
}

func (c *recordingWriteConn) WriteWebSocketMessage(payload []byte, messageType transport.WebSocketMessageType) error {
	c.messageCalls++
	c.writePayload = payload
	return c.writeErr
}

type webSocketMessageTypeConn struct {
	messageType  transport.WebSocketMessageType
	writeCalls   int
	messageCalls int
}

func (c *webSocketMessageTypeConn) ID() uint64         { return 3 }
func (c *webSocketMessageTypeConn) Close() error       { return nil }
func (c *webSocketMessageTypeConn) LocalAddr() string  { return "local" }
func (c *webSocketMessageTypeConn) RemoteAddr() string { return "remote" }

func (c *webSocketMessageTypeConn) Write([]byte) error {
	c.writeCalls++
	return nil
}

func (c *webSocketMessageTypeConn) WriteWebSocketMessage(_ []byte, messageType transport.WebSocketMessageType) error {
	c.messageCalls++
	c.messageType = messageType
	return nil
}

func BenchmarkServerEncodeAndWrite(b *testing.B) {
	encoded := []byte("encoded-payload")
	frameToWrite := &frame.PingPacket{}
	srv := &Server{}
	state := &sessionState{
		server:   srv,
		conn:     &recordingWriteConn{},
		session:  session.New(session.Config{ID: 5}),
		listener: &listenerRuntime{adapter: staticEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkEncodeWriteErr = srv.encodeAndWrite(state, frameToWrite, session.OutboundMeta{})
		if benchmarkEncodeWriteErr != nil {
			b.Fatalf("encodeAndWrite failed: %v", benchmarkEncodeWriteErr)
		}
	}
}
