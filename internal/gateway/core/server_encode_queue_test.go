package core

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var benchmarkEncodeQueueErr error

func TestServerEncodeAndQueueUsesOwnedEncodedQueue(t *testing.T) {
	encoded := []byte("encoded-payload")
	queue := &ownedEncodedQueueRecorder{}
	srv := &Server{}
	state := &sessionState{
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
		queue:    queue,
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("encodeAndQueue() error = %v", err)
	}
	if queue.ownedCalls != 1 {
		t.Fatalf("owned enqueue calls = %d, want 1", queue.ownedCalls)
	}
	if queue.copyCalls != 0 {
		t.Fatalf("copy enqueue calls = %d, want 0", queue.copyCalls)
	}
	if len(queue.payload) == 0 || &queue.payload[0] != &encoded[0] {
		t.Fatal("encodeAndQueue copied encoded payload before queueing")
	}
}

func TestServerEncodeAndQueueStartsWriterOnDemand(t *testing.T) {
	encoded := []byte("encoded-payload")
	conn := &blockingWriteConn{release: make(chan struct{})}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               1,
		WriteQueueSize:   1,
		MaxOutboundBytes: 64,
	})
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    sess.(session.EncodedQueue),
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("encodeAndQueue() error = %v", err)
	}
	waitForCoreTesting(t, func() bool { return conn.started.Load() })

	close(conn.release)
	if err := sess.Close(); err != nil {
		t.Fatalf("session close failed: %v", err)
	}
	srv.workerWG.Wait()
}

func TestServerEncodeAndQueueWritesDirectForTransportManagedConn(t *testing.T) {
	encoded := []byte("encoded-payload")
	conn := &transportManagedWriteConn{}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               2,
		WriteQueueSize:   1,
		MaxOutboundBytes: 64,
	})
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    sess.(session.EncodedQueue),
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("encodeAndQueue() error = %v", err)
	}
	if conn.writeCalls != 1 {
		t.Fatalf("write calls = %d, want 1", conn.writeCalls)
	}
	if conn.messageCalls != 0 {
		t.Fatalf("message calls = %d, want 0", conn.messageCalls)
	}
	if queued, ok := sess.(interface{ HasQueuedEncoded() bool }); ok && queued.HasQueuedEncoded() {
		t.Fatal("encodeAndQueue allocated write queue for transport-managed direct write")
	}
	if pending, ok := sess.(interface{ HasPendingEncoded() bool }); ok && pending.HasPendingEncoded() {
		t.Fatal("encodeAndQueue left pending encoded payloads for transport-managed direct write")
	}
	if !bytes.Equal(conn.writePayload, encoded) {
		t.Fatalf("write payload = %q, want %q", conn.writePayload, encoded)
	}
}

func TestServerEncodeAndQueueDoesNotBypassQueuedWrites(t *testing.T) {
	encoded := []byte("encoded-payload")
	conn := &transportManagedWriteConn{}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               3,
		WriteQueueSize:   2,
		MaxOutboundBytes: 64,
	})
	queue := sess.(session.EncodedQueue)
	if err := queue.EnqueueEncoded([]byte("queued")); err != nil {
		t.Fatalf("preload queue failed: %v", err)
	}
	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    queue,
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("encodeAndQueue() error = %v", err)
	}
	if conn.writeCalls != 0 {
		t.Fatalf("write calls = %d, want 0 when queued writes exist", conn.writeCalls)
	}
	if got := conn.messageCalls; got != 0 {
		t.Fatalf("message calls = %d, want 0", got)
	}
	first, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected preloaded payload to remain queued")
	}
	if string(first) != "queued" {
		t.Fatalf("first queued payload = %q, want %q", first, "queued")
	}
	second, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected encoded payload to be queued")
	}
	if string(second) != string(encoded) {
		t.Fatalf("queued encoded payload = %q, want %q", second, encoded)
	}
}

func TestServerEncodeAndQueueAllowsDirectWriteAfterQueuedWritesDrain(t *testing.T) {
	encoded := []byte("encoded-payload")
	conn := &transportManagedWriteConn{}
	srv := &Server{}
	sess := session.New(session.Config{
		ID:               4,
		WriteQueueSize:   2,
		MaxOutboundBytes: 64,
	})
	queue := sess.(session.EncodedQueue)
	if err := queue.EnqueueEncoded([]byte("queued")); err != nil {
		t.Fatalf("preload queue failed: %v", err)
	}
	payload, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected preloaded payload to dequeue")
	}
	queue.ReleaseEncoded(payload)

	state := &sessionState{
		server:   srv,
		conn:     conn,
		session:  sess,
		queue:    queue,
		listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
		closedCh: make(chan struct{}),
	}

	if err := srv.encodeAndQueue(state, &frame.PingPacket{}, session.OutboundMeta{}); err != nil {
		t.Fatalf("encodeAndQueue() error = %v", err)
	}
	if conn.writeCalls != 1 {
		t.Fatalf("write calls = %d, want 1 after drain", conn.writeCalls)
	}
	if conn.messageCalls != 0 {
		t.Fatalf("message calls = %d, want 0", conn.messageCalls)
	}
}

type ownedEncodeAdapter struct {
	encoded []byte
}

func (a ownedEncodeAdapter) Name() string { return "owned-encode" }

func (a ownedEncodeAdapter) Decode(session.Session, []byte) ([]frame.Frame, int, error) {
	return nil, 0, nil
}

func (a ownedEncodeAdapter) Encode(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error) {
	return a.encoded, nil
}

func (a ownedEncodeAdapter) OnOpen(session.Session) error  { return nil }
func (a ownedEncodeAdapter) OnClose(session.Session) error { return nil }

type transportManagedWriteConn struct {
	writeCalls   int
	messageCalls int
	writePayload []byte
}

func (c *transportManagedWriteConn) ID() uint64         { return 4 }
func (c *transportManagedWriteConn) Close() error       { return nil }
func (c *transportManagedWriteConn) LocalAddr() string  { return "local" }
func (c *transportManagedWriteConn) RemoteAddr() string { return "remote" }
func (c *transportManagedWriteConn) TransportManagedWriteTimeout() bool {
	return true
}

func (c *transportManagedWriteConn) Write(payload []byte) error {
	c.writeCalls++
	c.writePayload = payload
	return nil
}

func (c *transportManagedWriteConn) WriteWebSocketMessage(payload []byte, messageType transport.WebSocketMessageType) error {
	c.messageCalls++
	c.writePayload = payload
	return nil
}

func BenchmarkServerEncodeAndQueue(b *testing.B) {
	encoded := []byte("encoded-payload")
	frameToWrite := &frame.PingPacket{}
	for _, tc := range []struct {
		name   string
		direct bool
	}{
		{name: "queued", direct: false},
		{name: "direct", direct: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			srv := &Server{}
			sess := session.New(session.Config{
				ID:               5,
				WriteQueueSize:   2,
				MaxOutboundBytes: 64,
			})
			state := &sessionState{
				server:   srv,
				conn:     &transportManagedWriteConn{},
				session:  sess,
				queue:    sess.(session.EncodedQueue),
				listener: &listenerRuntime{adapter: ownedEncodeAdapter{encoded: encoded}},
				closedCh: make(chan struct{}),
			}
			if !tc.direct {
				state.openDispatching.Store(true)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkEncodeQueueErr = srv.encodeAndQueue(state, frameToWrite, session.OutboundMeta{})
				if benchmarkEncodeQueueErr != nil {
					b.Fatalf("encodeAndQueue failed: %v", benchmarkEncodeQueueErr)
				}
				if !tc.direct {
					payload, ok := state.queue.DequeueEncoded()
					if !ok {
						b.Fatal("dequeue failed")
					}
					state.queue.ReleaseEncoded(payload)
				}
			}
		})
	}
}

type ownedEncodedQueueRecorder struct {
	payload    []byte
	copyCalls  int
	ownedCalls int
}

func (q *ownedEncodedQueueRecorder) EnqueueEncoded(payload []byte) error {
	q.copyCalls++
	q.payload = append([]byte(nil), payload...)
	return nil
}

func (q *ownedEncodedQueueRecorder) EnqueueOwnedEncoded(payload []byte) error {
	q.ownedCalls++
	q.payload = payload
	return nil
}

func (q *ownedEncodedQueueRecorder) DequeueEncoded() ([]byte, bool) {
	return q.payload, q.payload != nil
}

func (q *ownedEncodedQueueRecorder) ReleaseEncoded([]byte) {}
