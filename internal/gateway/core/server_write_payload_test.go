package core

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var errWriteFromHelperGoroutine = errors.New("write called from timeout helper goroutine")

func TestServerWritePayloadDoesNotSpawnTimeoutGoroutineForTransportManagedWrites(t *testing.T) {
	conn := &sameGoroutineWriteConn{caller: currentGoroutineID(t)}
	srv := &Server{options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{WriteTimeout: time.Second}}}
	state := &sessionState{server: srv, conn: conn, closedCh: make(chan struct{})}

	if err := srv.writePayload(state, []byte("payload")); err != nil {
		t.Fatalf("writePayload() error = %v, want nil", err)
	}
}

func TestServerWritePayloadStillTimesOutBlockingConn(t *testing.T) {
	conn := &blockingWriteConn{release: make(chan struct{})}
	t.Cleanup(func() { close(conn.release) })
	srv := &Server{options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{WriteTimeout: time.Millisecond}}}
	state := &sessionState{server: srv, conn: conn, closedCh: make(chan struct{})}

	err := srv.writePayload(state, []byte("payload"))
	if !errors.Is(err, gatewaytypes.ErrWriteTimeout) {
		t.Fatalf("writePayload() error = %v, want %v", err, gatewaytypes.ErrWriteTimeout)
	}
}

func TestServerWritePayloadUsesWebSocketProtocolMessageType(t *testing.T) {
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

			if err := srv.writePayload(state, []byte("payload")); err != nil {
				t.Fatalf("writePayload() error = %v", err)
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

func TestServerStartWriterReleasesOutboundAccountingAfterWrite(t *testing.T) {
	conn := &blockingWriteConn{release: make(chan struct{})}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               4,
		WriteQueueSize:   2,
		MaxOutboundBytes: 3,
	})
	queue := sess.(session.EncodedQueue)
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    queue,
		closedCh: make(chan struct{}),
	}
	if err := queue.EnqueueEncoded([]byte("abc")); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	srv.startWriter(state)
	waitForCoreTesting(t, func() bool { return conn.started.Load() })
	if err := queue.EnqueueEncoded([]byte("d")); !errors.Is(err, session.ErrOutboundOverflow) {
		t.Fatalf("enqueue while write in-flight error = %v, want %v", err, session.ErrOutboundOverflow)
	}

	close(conn.release)
	waitForCoreTesting(t, func() bool {
		return queue.EnqueueEncoded([]byte("d")) == nil
	})
	_ = sess.Close()
	srv.workerWG.Wait()
}

func TestServerStartWriterExitsAndRestartsAfterIdle(t *testing.T) {
	conn := &countingWriteConn{}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               5,
		WriteQueueSize:   2,
		MaxOutboundBytes: 64,
	})
	queue := sess.(session.EncodedQueue)
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    queue,
		closedCh: make(chan struct{}),
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: []byte("payload")}},
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("first encodeAndQueue() error = %v", err)
	}
	waitForCoreTesting(t, func() bool { return conn.writeCalls.Load() == 1 })
	waitForCoreTesting(t, func() bool {
		writer, ok := sess.(interface{ WriterRunning() bool })
		return ok && !writer.WriterRunning()
	})

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("second encodeAndQueue() error = %v", err)
	}
	waitForCoreTesting(t, func() bool { return conn.writeCalls.Load() == 2 })
}

type sameGoroutineWriteConn struct {
	caller uint64
}

func (c *sameGoroutineWriteConn) ID() uint64                         { return 1 }
func (c *sameGoroutineWriteConn) Close() error                       { return nil }
func (c *sameGoroutineWriteConn) LocalAddr() string                  { return "local" }
func (c *sameGoroutineWriteConn) RemoteAddr() string                 { return "remote" }
func (c *sameGoroutineWriteConn) TransportManagedWriteTimeout() bool { return true }

func (c *sameGoroutineWriteConn) Write([]byte) error {
	if currentGoroutineID(nil) != c.caller {
		return errWriteFromHelperGoroutine
	}
	return nil
}

type blockingWriteConn struct {
	release chan struct{}
	started atomic.Bool
}

func (c *blockingWriteConn) ID() uint64         { return 2 }
func (c *blockingWriteConn) Close() error       { return nil }
func (c *blockingWriteConn) LocalAddr() string  { return "local" }
func (c *blockingWriteConn) RemoteAddr() string { return "remote" }

func (c *blockingWriteConn) Write([]byte) error {
	c.started.Store(true)
	<-c.release
	return nil
}

type countingWriteConn struct {
	writeCalls atomic.Int64
}

func (c *countingWriteConn) ID() uint64         { return 6 }
func (c *countingWriteConn) Close() error       { return nil }
func (c *countingWriteConn) LocalAddr() string  { return "local" }
func (c *countingWriteConn) RemoteAddr() string { return "remote" }

func (c *countingWriteConn) Write([]byte) error {
	c.writeCalls.Add(1)
	return nil
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

func currentGoroutineID(t *testing.T) uint64 {
	if t != nil {
		t.Helper()
	}
	buf := make([]byte, 64)
	n := runtime.Stack(buf, false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		if t != nil {
			t.Fatalf("unexpected goroutine stack header: %q", buf[:n])
		}
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		if t != nil {
			t.Fatalf("parse goroutine id from %q: %v", fields[1], err)
		}
		return 0
	}
	return id
}

func waitForCoreTesting(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
