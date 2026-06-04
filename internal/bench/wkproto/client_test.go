package wkproto

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
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

func TestClientReadFrameRetainsPartialFrameAfterTimeout(t *testing.T) {
	packet := &frame.SendackPacket{
		MessageID:   11,
		MessageSeq:  12,
		ClientSeq:   13,
		ClientMsgNo: "client-13",
		ReasonCode:  frame.ReasonSuccess,
	}
	encoded := mustEncodeFrame(t, packet)
	headerLen := wkprotoHeaderLen(t, encoded)
	conn := newTimeoutAfterReadConn(encoded, headerLen+3)
	client, err := NewClient(ClientConfig{
		Addr:             "127.0.0.1:5100",
		Dialer:           staticDialer{conn: conn},
		OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.mu.Lock()
	client.conn = conn
	client.mu.Unlock()
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.ReadFrame(ctx); err == nil {
		t.Fatal("first ReadFrame() error = nil, want timeout after partial frame")
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("second ReadFrame() error = %v", err)
	}
	ack, ok := f.(*frame.SendackPacket)
	if !ok {
		t.Fatalf("second ReadFrame() = %T, want *frame.SendackPacket", f)
	}
	if ack.ClientMsgNo != packet.ClientMsgNo || ack.ClientSeq != packet.ClientSeq || ack.MessageSeq != packet.MessageSeq {
		t.Fatalf("sendack = %#v, want %#v", ack, packet)
	}
}

func TestClientConnectStartsBackgroundReader(t *testing.T) {
	ack1 := &frame.SendackPacket{
		MessageID:   11,
		MessageSeq:  12,
		ClientSeq:   13,
		ClientMsgNo: "client-13",
		ReasonCode:  frame.ReasonSuccess,
	}
	ack2 := &frame.SendackPacket{
		MessageID:   21,
		MessageSeq:  22,
		ClientSeq:   23,
		ClientMsgNo: "client-23",
		ReasonCode:  frame.ReasonSuccess,
	}
	conn := newChunkedReadConn([][]byte{
		mustEncodeFrame(t, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion}),
		append(mustEncodeFrame(t, ack1), mustEncodeFrame(t, ack2)...),
	})
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
	if !conn.waitReads(2, time.Second) {
		t.Fatal("background reader did not consume queued sendacks")
	}

	for _, want := range []*frame.SendackPacket{ack1, ack2} {
		f, err := client.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame() error = %v", err)
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			t.Fatalf("ReadFrame() = %T, want *frame.SendackPacket", f)
		}
		if ack.ClientSeq != want.ClientSeq || ack.ClientMsgNo != want.ClientMsgNo || ack.MessageSeq != want.MessageSeq {
			t.Fatalf("sendack = %#v, want %#v", ack, want)
		}
	}
}

func TestClientReadFrameReportsRecvDecryptContext(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
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
		writeFrame(t, conn, &frame.RecvPacket{
			MessageID:   99,
			MessageSeq:  7,
			ClientMsgNo: "m1",
			FromUID:     "u2",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("plain-payload"),
		})
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

	_, err = client.ReadFrame(ctx)
	if err == nil {
		t.Fatal("ReadFrame() error = nil, want decrypt context")
	}
	msg := err.Error()
	for _, want := range []string{
		"decrypt recv payload",
		`channel_id="g1"`,
		"channel_type=2",
		`client_msg_no="m1"`,
		"msg_key_empty=true",
		`payload_prefix="plain-payload"`,
		"illegal base64",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ReadFrame() error %q missing %q", msg, want)
		}
	}
}

func TestClientReadFrameDetachesRecvPayloadBeforeBufferCompaction(t *testing.T) {
	keys := protocolenc.SessionKeys{
		AESKey: []byte("0123456789abcdef"),
		AESIV:  []byte("abcdefghijklmnop"),
	}
	sessionCrypto, err := protocolenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}
	sealed, err := protocolenc.SealRecvPacketWithCrypto(&frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  7,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u2",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("welcome"),
	}, sessionCrypto)
	if err != nil {
		t.Fatalf("SealRecvPacketWithCrypto() error = %v", err)
	}
	wire := append(mustEncodeFrame(t, sealed), bytes.Repeat([]byte{0x05}, 512)...)
	conn := newPartialWriteConn(wire, 0)
	client, err := NewClient(ClientConfig{
		Addr:             "127.0.0.1:5100",
		Dialer:           staticDialer{conn: conn},
		OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.mu.Lock()
	client.conn = conn
	client.sessionCrypto = sessionCrypto
	client.mu.Unlock()
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
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

func wkprotoHeaderLen(t *testing.T, payload []byte) int {
	t.Helper()
	if len(payload) < 2 {
		t.Fatalf("encoded frame too short: %d", len(payload))
	}
	idx := 1
	for ; idx < len(payload); idx++ {
		if payload[idx] < 0x80 {
			return idx + 1
		}
	}
	t.Fatalf("encoded frame missing remaining length terminator")
	return 0
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

type timeoutAfterReadConn struct {
	mu           sync.Mutex
	data         []byte
	offset       int
	timeoutAfter int
	timedOut     bool
	closed       bool
}

type chunkedReadConn struct {
	mu       sync.Mutex
	chunks   [][]byte
	index    int
	closed   bool
	readCh   chan struct{}
	writeBuf []byte
}

func newChunkedReadConn(chunks [][]byte) *chunkedReadConn {
	copied := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		copied = append(copied, append([]byte(nil), chunk...))
	}
	return &chunkedReadConn{chunks: copied, readCh: make(chan struct{}, len(copied)+1)}
}

func (c *chunkedReadConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if c.index >= len(c.chunks) {
		return 0, io.EOF
	}
	chunk := c.chunks[c.index]
	n := copy(p, chunk)
	if n == len(chunk) {
		c.index++
	} else {
		c.chunks[c.index] = chunk[n:]
	}
	select {
	case c.readCh <- struct{}{}:
	default:
	}
	return n, nil
}

func (c *chunkedReadConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	c.writeBuf = append(c.writeBuf, p...)
	return len(p), nil
}

func (c *chunkedReadConn) waitReads(want int, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	reads := 0
	for reads < want {
		select {
		case <-c.readCh:
			reads++
		case <-timer.C:
			return false
		}
	}
	return true
}

func (c *chunkedReadConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *chunkedReadConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *chunkedReadConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *chunkedReadConn) SetDeadline(time.Time) error {
	return nil
}

func (c *chunkedReadConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *chunkedReadConn) SetWriteDeadline(time.Time) error {
	return nil
}

func newTimeoutAfterReadConn(data []byte, timeoutAfter int) *timeoutAfterReadConn {
	return &timeoutAfterReadConn{data: append([]byte(nil), data...), timeoutAfter: timeoutAfter}
}

func (c *timeoutAfterReadConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if c.offset >= len(c.data) {
		return 0, io.EOF
	}
	limit := len(c.data)
	if !c.timedOut && c.timeoutAfter > 0 && c.timeoutAfter < limit {
		limit = c.timeoutAfter
	}
	if c.offset >= limit {
		c.timedOut = true
		return 0, timeoutError{}
	}
	n := copy(p, c.data[c.offset:limit])
	c.offset += n
	if !c.timedOut && c.timeoutAfter > 0 && c.offset >= c.timeoutAfter {
		c.timedOut = true
		return n, timeoutError{}
	}
	return n, nil
}

func (c *timeoutAfterReadConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p), nil
}

func (c *timeoutAfterReadConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *timeoutAfterReadConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *timeoutAfterReadConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *timeoutAfterReadConn) SetDeadline(time.Time) error {
	return nil
}

func (c *timeoutAfterReadConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *timeoutAfterReadConn) SetWriteDeadline(time.Time) error {
	return nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

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
