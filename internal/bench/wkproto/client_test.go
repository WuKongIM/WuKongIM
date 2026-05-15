package wkproto

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestClientConnectSendsConnectPacketAndAcceptsConnack(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		if connect.Version != frame.LatestVersion {
			t.Fatalf("connect.Version = %d, want %d", connect.Version, frame.LatestVersion)
		}
		if connect.UID != "u1" || connect.DeviceID != "d1" || connect.DeviceFlag != frame.APP {
			t.Fatalf("connect identity = uid %q device %q flag %d", connect.UID, connect.DeviceID, connect.DeviceFlag)
		}
		if connect.ClientTimestamp <= 0 {
			t.Fatal("connect.ClientTimestamp was not set")
		}
		if connect.ClientKey == "" {
			t.Fatal("connect.ClientKey is empty")
		}
		if connect.Token != "auth-token" {
			t.Fatalf("connect.Token = %q, want %q", connect.Token, "auth-token")
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, Token: "auth-token", OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestClientEncryptsSendDecryptsRecvAndWritesRecvAck(t *testing.T) {
	serverDone := make(chan struct{})
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		defer close(serverDone)

		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		connect := f.(*frame.ConnectPacket)
		serverKeys, serverKey, err := protocolenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			t.Fatalf("NegotiateServerSession() error = %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{
			ReasonCode:    frame.ReasonSuccess,
			ServerVersion: frame.LatestVersion,
			ServerKey:     serverKey,
			Salt:          string(serverKeys.AESIV),
		})

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode send: %v", err)
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			t.Fatalf("second frame = %T, want *frame.SendPacket", f)
		}
		if got := string(send.Payload); got == "hello" {
			t.Fatalf("send payload was not encrypted: %q", got)
		}
		if send.MsgKey == "" {
			t.Fatal("send MsgKey is empty")
		}
		if err := protocolenc.ValidateSendPacket(send, serverKeys); err != nil {
			t.Fatalf("ValidateSendPacket() error = %v", err)
		}
		plain, err := protocolenc.DecryptPayload(send.Payload, serverKeys)
		if err != nil {
			t.Fatalf("DecryptPayload() error = %v", err)
		}
		if got, want := string(plain), "hello"; got != want {
			t.Fatalf("send plaintext = %q, want %q", got, want)
		}

		recv, err := protocolenc.SealRecvPacket(&frame.RecvPacket{
			MessageID:   99,
			MessageSeq:  7,
			ClientMsgNo: "m1",
			Timestamp:   123,
			FromUID:     "u2",
			ChannelID:   "u1",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("welcome"),
		}, serverKeys)
		if err != nil {
			t.Fatalf("SealRecvPacket() error = %v", err)
		}
		writeFrame(t, conn, recv)

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode recvack: %v", err)
		}
		ack, ok := f.(*frame.RecvackPacket)
		if !ok {
			t.Fatalf("third frame = %T, want *frame.RecvackPacket", f)
		}
		if ack.MessageID != 99 || ack.MessageSeq != 7 {
			t.Fatalf("recvack = (%d,%d), want (99,7)", ack.MessageID, ack.MessageSeq)
		}
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Send(ctx, &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "c1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("ReadFrame() = %T, want *frame.RecvPacket", f)
	}
	if got, want := string(recv.Payload), "welcome"; got != want {
		t.Fatalf("recv payload = %q, want %q", got, want)
	}
	if err := client.RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
		t.Fatalf("RecvAck() error = %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not observe recvack")
	}
}

func TestClientSerializesConcurrentPartialWrites(t *testing.T) {
	conn := newPartialWriteConn(mustEncodeFrame(t, &frame.ConnackPacket{
		ReasonCode:    frame.ReasonSuccess,
		ServerVersion: frame.LatestVersion,
	}), 1)
	client, err := NewClient(ClientConfig{
		Addr:             "127.0.0.1:5100",
		Dialer:           staticDialer{conn: conn},
		OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	conn.resetWrites()

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- client.Send(ctx, &frame.SendPacket{
			ClientSeq:   1,
			ClientMsgNo: "c1",
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("hello"),
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- client.RecvAck(ctx, 99, 7)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write error = %v", err)
		}
	}
	if got := conn.maxConcurrentWrites(); got != 1 {
		t.Fatalf("max concurrent Write calls = %d, want 1", got)
	}

	frames := decodeWrittenFrames(t, conn.written())
	if len(frames) != 2 {
		t.Fatalf("decoded frames = %d, want 2", len(frames))
	}
	seen := map[frame.FrameType]bool{}
	for _, f := range frames {
		seen[f.GetFrameType()] = true
	}
	if !seen[frame.SEND] || !seen[frame.RECVACK] {
		t.Fatalf("decoded frame types = %#v, want SEND and RECVACK", seen)
	}
}

func TestClientMethodsReportNotConnectedForNilClient(t *testing.T) {
	var client *Client
	if err := client.Send(context.Background(), &frame.SendPacket{}); err == nil {
		t.Fatal("Send() error = nil, want not connected")
	}
	if _, err := client.ReadFrame(context.Background()); err == nil {
		t.Fatal("ReadFrame() error = nil, want not connected")
	}
	if err := client.RecvAck(context.Background(), 1, 1); err == nil {
		t.Fatal("RecvAck() error = nil, want not connected")
	}
}

type fakeWKProtoServer struct {
	addr string
	ln   net.Listener
	done chan struct{}
}

func newFakeWKProtoServer(t *testing.T, serve func(*testing.T, net.Conn)) *fakeWKProtoServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeWKProtoServer{addr: ln.Addr().String(), ln: ln, done: make(chan struct{})}
	go func() {
		defer close(server.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		serve(t, conn)
	}()
	return server
}

func (s *fakeWKProtoServer) close() {
	_ = s.ln.Close()
	<-s.done
}

func writeFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()
	payload := mustEncodeFrame(t, f)
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write frame %T: %v", f, err)
	}
}

func mustEncodeFrame(t *testing.T, f frame.Frame) []byte {
	t.Helper()
	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(%T): %v", f, err)
	}
	return payload
}

func decodeWrittenFrames(t *testing.T, payload []byte) []frame.Frame {
	t.Helper()
	var frames []frame.Frame
	proto := codec.New()
	for len(payload) > 0 {
		f, n, err := proto.DecodeFrame(payload, frame.LatestVersion)
		if err != nil {
			t.Fatalf("DecodeFrame() error = %v", err)
		}
		if f == nil || n == 0 {
			t.Fatalf("DecodeFrame() consumed %d bytes and returned %T", n, f)
		}
		frames = append(frames, f)
		payload = payload[n:]
	}
	return frames
}

type staticDialer struct {
	conn net.Conn
}

func (d staticDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

type partialWriteConn struct {
	mu       sync.Mutex
	readBuf  []byte
	writeBuf []byte
	partial  int
	closed   bool
	active   int32
	max      int32
}

func newPartialWriteConn(readBuf []byte, partial int) *partialWriteConn {
	return &partialWriteConn{readBuf: append([]byte(nil), readBuf...), partial: partial}
}

func (c *partialWriteConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if len(c.readBuf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *partialWriteConn) Write(p []byte) (int, error) {
	current := atomic.AddInt32(&c.active, 1)
	c.recordMaxConcurrent(current)
	defer atomic.AddInt32(&c.active, -1)

	if len(p) == 0 {
		return 0, nil
	}
	n := len(p)
	if c.partial > 0 && c.partial < n {
		n = c.partial
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	c.writeBuf = append(c.writeBuf, p[:n]...)
	c.mu.Unlock()
	time.Sleep(time.Millisecond)
	return n, nil
}

func (c *partialWriteConn) recordMaxConcurrent(current int32) {
	for {
		max := atomic.LoadInt32(&c.max)
		if current <= max || atomic.CompareAndSwapInt32(&c.max, max, current) {
			return
		}
	}
}

func (c *partialWriteConn) resetWrites() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeBuf = nil
	atomic.StoreInt32(&c.max, 0)
}

func (c *partialWriteConn) written() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.writeBuf...)
}

func (c *partialWriteConn) maxConcurrentWrites() int32 {
	return atomic.LoadInt32(&c.max)
}

func (c *partialWriteConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *partialWriteConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *partialWriteConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *partialWriteConn) SetDeadline(time.Time) error {
	return nil
}

func (c *partialWriteConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *partialWriteConn) SetWriteDeadline(time.Time) error {
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return "tcp"
}

func (a fakeAddr) String() string {
	return string(a)
}
